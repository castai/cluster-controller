# Cluster-Tunnel Simplification Analysis

## Executive Summary

The current implementation is a custom HTTP-over-gRPC reverse tunnel (~460 lines of Go + ~480 lines of tests + ~44 lines proto). It works, but it **reinvents HTTP/2 multiplexing semantics** over a single bidirectional gRPC stream. The primary complexity comes from:

1. **Manual request/response multiplexing** via `request_id` on a shared stream
2. **Mutex-guarded stream writer** needed because multiple goroutines write to one stream
3. **Three-message response protocol** (Start/Body/End) that reimplements HTTP/2 framing
4. **Custom heartbeat** that duplicates gRPC's built-in keepalive

All four can be eliminated by changing the gRPC contract.

---

## Current State: Complexity Map

| Component | Lines | Complexity Source |
|-----------|-------|-------------------|
| `client.go` (business logic) | 315 | StreamWriter mutex, 3-phase response, heartbeat loop, multiplexing |
| `client_test.go` | 478 | `findStart/collectBody/hasEnd` helpers to reassemble multiplexed responses |
| `command.go` (wiring) | 149 | Fine as-is |
| `proxy.proto` | 44 | 6 message types for what is essentially: request in, response out |

The core issue: **one bidirectional stream carries N concurrent request/response pairs**, so every message needs a `request_id`, responses need 3 message types (Start → Body chunks → End) to delimit boundaries, and all sends need a mutex.

---

## Recommendation: Split into Two RPCs

Replace the single bidirectional stream with two simpler RPCs:

```protobuf
syntax = "proto3";
package clustertunnel.v1alpha1;

import "google/protobuf/empty.proto";

service ClusterTunnel {
  // Agent subscribes to incoming HTTP requests (server-streaming).
  // Cluster ID is sent as gRPC metadata, same as today.
  // gRPC keepalive replaces the custom heartbeat.
  rpc Subscribe(google.protobuf.Empty) returns (stream HttpRequest);

  // Agent sends back one HTTP response per request (client-streaming).
  // request_id sent as gRPC metadata — not repeated in every chunk.
  rpc SendResponse(stream HttpResponse) returns (google.protobuf.Empty);
}

message HttpRequest {
  string request_id = 1;
  string method     = 2;
  string path       = 3;
  repeated Header headers = 4;
  bytes body = 5;
}

message HttpResponse {
  int32 status_code     = 1;  // Set in first message only (0 in subsequent chunks)
  repeated Header headers = 2; // Set in first message only (empty in subsequent chunks)
  bytes body             = 3;  // Chunk data
}

message Header {
  string key   = 1;
  string value = 2;
}
```

### What this eliminates

| Removed | Why it's no longer needed |
|---------|--------------------------|
| `StreamWriter` + mutex | Each `SendResponse` is its own gRPC stream — no shared writer |
| `request_id` on every response message | Sent once as gRPC metadata on `SendResponse` call |
| `HttpResponseStart` / `HttpResponseBody` / `HttpResponseEnd` | Collapsed into single `HttpResponse`; stream close = end |
| `Heartbeat` message | `grpc.WithKeepaliveParams()` on the `Subscribe` stream |
| `heartbeatLoop()` goroutine | Deleted entirely |
| `findStart/collectBody/hasEnd` test helpers | No multiplexed response stream to reassemble |

### Agent code sketch (full replacement of client.go core)

```go
func (c *Client) handleStream(ctx context.Context, reqStream pb.ClusterTunnel_SubscribeClient) error {
    g, ctx := errgroup.WithContext(ctx)

    for {
        req, err := reqStream.Recv()
        if err != nil {
            return fmt.Errorf("receiving request: %w", err)
        }

        g.Go(func() error {
            c.proxyRequest(ctx, req)
            return nil
        })
    }
}

func (c *Client) proxyRequest(ctx context.Context, req *pb.HttpRequest) {
    md := metadata.Pairs("x-request-id", req.RequestId)
    respCtx := metadata.NewOutgoingContext(ctx, md)

    respStream, err := c.tunnelClient.SendResponse(respCtx)
    if err != nil {
        c.log.WithError(err).Error("opening response stream")
        return
    }
    defer respStream.CloseAndRecv()

    httpResp, err := c.doHTTPRequest(ctx, req)
    if err != nil {
        c.sendError(respStream, http.StatusBadGateway, err.Error())
        return
    }
    defer httpResp.Body.Close()

    // First message: status + headers
    respStream.Send(&pb.HttpResponse{
        StatusCode: int32(httpResp.StatusCode),
        Headers:    convertHeaders(httpResp.Header),
    })

    // Subsequent messages: body chunks
    buf := make([]byte, 32*1024)
    for {
        n, readErr := httpResp.Body.Read(buf)
        if n > 0 {
            respStream.Send(&pb.HttpResponse{Body: buf[:n]})
        }
        if readErr != nil {
            break
        }
    }
    // Stream close signals end — no explicit "End" message needed
}
```

### Why this is better

1. **~40% less agent code**. No mutex, no heartbeat goroutine, no 3-type response dispatch, no request_id bookkeeping on every message.

2. **Per-request flow control**. gRPC manages backpressure independently for each `SendResponse` stream. With the current design, one slow response blocks the shared stream's send buffer for all responses.

3. **Per-request error isolation**. If `SendResponse` for request A fails, it doesn't tear down the `Subscribe` stream. Currently, a `Send()` failure on the shared stream kills everything.

4. **Simpler server side too**. The server doesn't need to reassemble interleaved Start/Body/End messages from a single stream. Each `SendResponse` stream is one complete response.

5. **Native gRPC keepalive** replaces custom heartbeat:
```go
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:                30 * time.Second,
    Timeout:             10 * time.Second,
    PermitWithoutStream: true, // keep conn alive even when no Subscribe stream
})
```

### Cost / tradeoff

- The server handles N+1 gRPC streams per cluster (1 Subscribe + N concurrent SendResponse). For 10 concurrent requests this is 11 HTTP/2 streams on one TCP connection — well within normal gRPC operation.
- The server registry becomes slightly different: it tracks a Subscribe stream (to push requests) and matches incoming `SendResponse` calls via `x-request-id` metadata.
- Requires proto contract change (acceptable since nothing is in prod).

---

## Alternative: Simplified Single-Stream (minimal change)

If you prefer to keep the bidirectional stream pattern, these changes still cut complexity significantly:

### 1. Replace custom heartbeat with gRPC keepalive

Delete `heartbeatLoop()`, `Heartbeat` message, and `HeartbeatInterval` config. Add:

```go
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:    30 * time.Second,
    Timeout: 10 * time.Second,
})
```

Server side:
```go
grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
    MinTime:             15 * time.Second,
    PermitWithoutStream: true,
})
```

**Saves**: ~25 lines of client code, removes Heartbeat message from proto, removes heartbeat tests.

### 2. Merge three response types into one

```protobuf
message TunnelMessage {
  oneof payload {
    HttpRequest  http_request  = 1;
    HttpResponse http_response = 2;
  }
}

message HttpResponse {
  string request_id         = 1;
  int32 status_code         = 2; // Non-zero in first chunk only
  repeated Header headers   = 3; // Present in first chunk only
  bytes body                = 4;
  bool done                 = 5; // True on final chunk
}
```

**Saves**: ~30 lines of client code (sendErrorResponse simplifies, doProxy send logic simplifies), removes 2 message types from proto, eliminates `findStart/collectBody/hasEnd` test helpers.

### 3. Combined savings for single-stream simplification

~55 lines less in client.go, cleaner proto, simpler tests. StreamWriter mutex remains necessary.

---

## Code-Level Improvements (applicable to any approach)

### 1. gRPC connection lifecycle

Current code creates a new `grpc.ClientConn` on every reconnect:

```go
// current: connect() creates conn, defers Close, opens stream
conn, err := grpc.NewClient(c.cfg.Address, dialOpts...)
defer conn.Close()
stream, err := client.Connect(streamCtx)
```

Better: create the connection once, only recreate the stream on failure. gRPC connections are designed to be long-lived and handle transient failures internally.

```go
func (c *Client) Run(ctx context.Context) error {
    conn, err := grpc.NewClient(c.cfg.Address, c.grpcDialOptions()...)
    if err != nil {
        return fmt.Errorf("creating gRPC connection: %w", err)
    }
    defer conn.Close()

    client := pb.NewClusterTunnelClient(conn)

    // Only reconnect the stream, not the entire connection
    return waitext.Retry(ctx, boff, waitext.Forever, func(ctx context.Context) (bool, error) {
        stream, err := client.Subscribe(streamCtx)
        if err != nil {
            return true, err
        }
        return true, c.handleStream(ctx, stream)
    }, nil)
}
```

### 2. Use `errgroup` for request goroutines

Current code uses bare `go c.proxyRequest(...)` with no way to track completion or propagate errors. `errgroup` provides structured concurrency:

```go
g, ctx := errgroup.WithContext(ctx)
for {
    req, err := stream.Recv()
    if err != nil { break }
    g.Go(func() error {
        c.proxyRequest(ctx, req)
        return nil
    })
}
return g.Wait()
```

The errgroup also lets you limit concurrency with `g.SetLimit(10)` if needed.

### 3. Avoid `connected` flag + mutex for health checks

The `IsConnected()` / `setConnected()` pattern uses a mutex to protect a bool. Simpler with `atomic.Bool`:

```go
type Client struct {
    // ...
    connected atomic.Bool
}

func (c *Client) IsConnected() bool { return c.connected.Load() }
```

---

## Open-Source Assessment

| Project | Fits? | Reason |
|---------|-------|--------|
| **Konnectivity** (kubernetes-sigs/apiserver-network-proxy) | Partial | Same pattern (reverse HTTP-over-gRPC), but heavily coupled to K8s API server machinery. Massive dependency for a ~300-line problem. |
| **jhump/grpctunnel** | Interesting | Reverse gRPC tunnel — agent exposes a gRPC service through the tunnel. Eliminates ALL custom protocol code. But: 118 stars, tunnels gRPC not HTTP, so you'd still need a proxy service definition. |
| **Chisel** | No | WebSocket transport, not gRPC. Violates your company-wide gRPC constraint. |
| **inlets** | No | WebSocket transport, now commercial/archived. |

**Verdict**: No off-the-shelf gRPC library solves this cleanly enough to justify the dependency. The two-RPC approach reduces the agent to ~150 lines of straightforward gRPC client-streaming code, which is simple enough that a library would add more complexity than it saves.

---

## Server-Side Notes

Even though the server lives in another repo, a few observations:

1. **Registry simplification with two-RPC**: The server no longer needs to reassemble interleaved `Start/Body/End` messages from a single stream. Each `SendResponse` stream is one complete response — the server just pipes it to the waiting HTTP client as chunks arrive.

2. **Heartbeat/liveness**: With gRPC keepalive, the server detects dead connections via transport-level PING failures instead of application-level heartbeat tracking. Delete the `HEARTBEAT_STALE_AFTER` cleanup logic.

3. **Idle timeout on proxy**: The server's "idle timeout that resets on each chunk" can be replaced with a simple context deadline on the `SendResponse` handler, plus gRPC's built-in stream timeout.

---

## Summary: Recommended Changes

| # | Change | Impact | Effort |
|---|--------|--------|--------|
| 1 | **Split into Subscribe + SendResponse RPCs** | Eliminates multiplexing, mutex, 3 response types | Medium (proto change + agent rewrite + server adaptation) |
| 2 | **Replace custom heartbeat with gRPC keepalive** | Removes heartbeat code on both sides | Small |
| 3 | **Long-lived `grpc.ClientConn`, reconnect stream only** | Better connection management, follows gRPC best practices | Small |
| 4 | **Use `errgroup` for request goroutines** | Structured concurrency, optional concurrency limit | Small |
| 5 | **`atomic.Bool` for connected flag** | Remove mutex for trivial flag | Trivial |

Changes 1+2 together yield approximately **40-50% less code** on the agent side, a cleaner proto contract (3 messages instead of 7), and better reliability characteristics (per-request isolation, native keepalive, no shared-stream contention).

---

## Alignment with kvisor Patterns

Reference: `kvisor/pkg/castai/client.go`

### Align with kvisor

**1. Auth: `x-cluster-id` + `authorization: Token <api-key>` via interceptors**

Current tunnel only sends `x-cluster-id`, manually injected per call, with no API key. kvisor sends both via gRPC interceptors on `NewClient()` so every RPC (unary and streaming) gets auth automatically.

Change:
- Add `API_KEY` to tunnel config
- Add `authorization: Token <api-key>` metadata
- Move metadata injection to interceptors on `grpc.NewClient()`
- With two-RPC split this is critical: both `Subscribe` and `SendResponse` need auth, and interceptors handle it automatically

```go
grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
    return invoker(withAuth(ctx, cfg), method, req, reply, cc, opts...)
}),
grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
    return streamer(withAuth(ctx, cfg), desc, cc, method, opts...)
}),

func withAuth(ctx context.Context, cfg Config) context.Context {
    return metadata.AppendToOutgoingContext(ctx,
        "x-cluster-id", cfg.ClusterID,
        "authorization", fmt.Sprintf("Token %s", cfg.APIKey),
    )
}
```

**2. Connection lifecycle: create `grpc.ClientConn` once, reuse**

Current tunnel creates a new `grpc.ClientConn` on every reconnect. kvisor creates it once in `NewClient()` and reuses for the process lifetime. This is also a gRPC best practice — `grpc.ClientConn` handles transport-level reconnection, DNS re-resolution, and TLS session resumption internally.

Change: create connection in `NewClient()`, store in `Client`, only reconnect the stream (Subscribe) on failure.

**3. No custom heartbeat — gRPC keepalive**

kvisor has zero heartbeat code. The tunnel should delete `heartbeatLoop()`, `Heartbeat` proto message, and `HeartbeatInterval` config. Replace with:

```go
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:                30 * time.Second,
    Timeout:             10 * time.Second,
    PermitWithoutStream: true,
})
```

**4. Compression: gzip on gRPC calls**

kvisor defaults to gzip on all RPCs. The tunnel sends uncompressed Kubernetes API JSON responses that compress very well. One line:

```go
grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name))
```

### Tunnel-specific (no kvisor equivalent)

**5. Request validation (read-only enforcement)**

The tunnel is a trust boundary — it receives requests from the backend and executes them against the customer's Kubernetes API. Unlike kvisor (which only pushes data), the tunnel must enforce read-only access. Reject non-GET methods, block `/exec`, `/attach`, `/portforward` paths.

```go
func (c *Client) validateRequest(req *pb.HttpRequest) error {
    if req.Method != http.MethodGet {
        return fmt.Errorf("method %s not allowed", req.Method)
    }
    for _, blocked := range []string{"/exec", "/attach", "/portforward"} {
        if strings.Contains(req.Path, blocked) {
            return fmt.Errorf("path containing %s is blocked", blocked)
        }
    }
    return nil
}
```

**6. `errgroup` for goroutine management**

kvisor uses a different execution model (controller loop). The tunnel spawns concurrent goroutines per request — `errgroup` gives structured concurrency and `SetLimit(10)` to cap concurrent requests.

**7. `atomic.Bool` for connected health check flag**

kvisor doesn't expose connection-state health. The tunnel needs it for Kubernetes liveness probes. Replace `sync.Mutex` + `bool` with `atomic.Bool`.

---

## Comparison: Current Tunnel vs kvisor-Aligned Tunnel

| Aspect | Current tunnel | kvisor | Proposed tunnel |
|--------|---------------|--------|-----------------|
| Auth metadata | `x-cluster-id` only, manual | `x-cluster-id` + `Token`, interceptors | `x-cluster-id` + `Token`, interceptors |
| Connection lifecycle | New `grpc.ClientConn` per retry | Create once, reuse | Create once, reuse |
| Heartbeat | Custom `Heartbeat` message + goroutine | None (gRPC keepalive) | None (gRPC keepalive) |
| Compression | None | gzip | gzip |
| Stream pattern | Single bidi, manual multiplexing | Client-streaming (`LogsWriteStream`) | Server-streaming + client-streaming |
| Connected flag | `sync.Mutex` + `bool` | N/A | `atomic.Bool` |
| Goroutine mgmt | Bare `go` | Controller loop | `errgroup` with `SetLimit(10)` |
| Request validation | None | N/A (push-only) | Read-only enforcement |

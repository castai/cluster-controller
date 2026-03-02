# Deep Technical Analysis: jhump/grpctunnel

**Date:** 2026-03-02
**Library:** github.com/jhump/grpctunnel v0.3.0
**Author:** Josh Humphries (co-founder, Buf Technologies)

---

## 1. Reverse Tunnel Capability

**Yes, the library has first-class reverse tunnel support.** This is not bolted on -- it is one of the two core use cases the library was designed for.

### How it works conceptually

The roles are inverted from the network perspective:

| Concept | Network role | Tunnel role |
|---|---|---|
| `ReverseTunnelServer` | Network **client** (the agent) | Exposes gRPC services for the server to call |
| `TunnelServiceHandler.AsChannel()` | Network **server** (the control plane) | Acts as a gRPC client to call services on the agent |

The agent (network client) connects outbound to the control plane server, opens an `OpenReverseTunnel` bidi stream, registers its gRPC service handlers, and calls `Serve()`. The server side can then call `handler.AsChannel()` to get a `grpc.ClientConnInterface` that routes RPCs *back through the tunnel* to the agent.

### Key types

```go
// Agent side (network client, tunnel server):
type ReverseTunnelServer struct { ... }
func NewReverseTunnelServer(stub tunnelpb.TunnelServiceClient, opts ...TunnelOption) *ReverseTunnelServer
func (s *ReverseTunnelServer) RegisterService(desc *grpc.ServiceDesc, srv interface{})
func (s *ReverseTunnelServer) Serve(ctx context.Context, opts ...grpc.CallOption) (started bool, err error)
func (s *ReverseTunnelServer) GracefulStop()
func (s *ReverseTunnelServer) Stop()

// Control plane side (network server, tunnel client):
type TunnelServiceHandler struct { ... }
func (s *TunnelServiceHandler) AsChannel() ReverseClientConnInterface           // round-robin across all agents
func (s *TunnelServiceHandler) KeyAsChannel(key interface{}) ReverseClientConnInterface  // affinity-keyed pool
func (s *TunnelServiceHandler) AllReverseTunnels() []TunnelChannel              // enumerate all

type ReverseClientConnInterface interface {
    grpc.ClientConnInterface
    Ready() bool
    WaitForReady(context.Context) error
}
```

### Affinity routing (multi-agent)

The `TunnelServiceHandlerOptions.AffinityKey` function lets the server extract a key from each incoming tunnel (e.g., cluster ID from metadata). Then `handler.KeyAsChannel("cluster-123")` returns a connection pool targeting only that agent's tunnels.

```go
handler := grpctunnel.NewTunnelServiceHandler(grpctunnel.TunnelServiceHandlerOptions{
    AffinityKey: func(ch grpctunnel.TunnelChannel) any {
        md, _ := grpctunnel.TunnelMetadataFromIncomingContext(ch.Context())
        if vals := md.Get("x-cluster-id"); len(vals) > 0 {
            return vals[0]
        }
        return ""
    },
    OnReverseTunnelOpen:  func(ch grpctunnel.TunnelChannel) { /* log/track */ },
    OnReverseTunnelClose: func(ch grpctunnel.TunnelChannel) { /* log/track */ },
})
```

---

## 2. Wire Protocol

### Transport layer

Both `OpenTunnel` (forward) and `OpenReverseTunnel` (reverse) use a **single bidirectional gRPC stream** as the transport. All multiplexed RPCs are carried over this one stream.

```protobuf
service TunnelService {
    rpc OpenTunnel(stream ClientToServer) returns (stream ServerToClient);
    rpc OpenReverseTunnel(stream ServerToClient) returns (stream ClientToServer);
}
```

Note: `OpenReverseTunnel` swaps the message direction -- the client sends `ServerToClient` and receives `ClientToServer`, reflecting the inverted roles.

### Multiplexing

Each tunneled RPC gets a monotonically-increasing 64-bit `stream_id`. The `ClientToServer` and `ServerToClient` messages carry this `stream_id` in every frame, enabling multiplexing of many concurrent RPCs over the single bidi stream.

### Frame types

**ClientToServer frames** (request side):
- `NewStream` -- opens a new RPC (method name + request headers + initial window size)
- `MessageData` -- sends a request message (chunked to 16KB to prevent head-of-line blocking)
- `more_request_data` -- continuation chunks for messages >16KB
- `half_close` -- signals no more request messages (like HTTP/2 END_STREAM on request)
- `cancel` -- aborts the stream
- `window_update` -- flow control credit (revision 1+)

**ServerToClient frames** (response side):
- `Settings` -- server capabilities (protocol revision, initial window size)
- `response_headers` -- gRPC response metadata
- `MessageData` -- response message (chunked to 16KB)
- `more_response_data` -- continuation chunks
- `CloseStream` -- RPC completion (trailers + status code)
- `window_update` -- flow control credit

### Flow control

Added in v0.2.0/v0.3.0 (protocol revision 1) to fix deadlock issues (#7, #8, #11):
- Per-stream flow control windows (default 64KB initial window)
- `window_update` frames replenish credits as data is consumed
- Prevents fast senders from overwhelming slow consumers
- Protocol negotiation via `grpctunnel-negotiate` header ensures backward compatibility with revision 0 peers
- Message chunking to 16KB prevents head-of-line blocking across multiplexed streams

### Protocol negotiation

Both client and server send a `grpctunnel-negotiate: on` header. If both sides support it, the server sends a `Settings` frame with `supported_protocol_revisions` and `initial_window_size`. If either side is revision 0, they fall back to no flow control.

---

## 3. API Surface

### Agent code is trivially simple

The agent registers standard gRPC service handlers. The library handles all tunnel plumbing:

```go
// Agent-side code (network client, reverse tunnel server):
conn, _ := grpc.NewClient("control-plane:443", grpc.WithTransportCredentials(creds))
stub := tunnelpb.NewTunnelServiceClient(conn)

revServer := grpctunnel.NewReverseTunnelServer(stub)
myservicepb.RegisterMyServiceServer(revServer, &myServiceImpl{})  // standard gRPC registration!

revServer.Serve(ctx)  // blocks, serving RPCs that come through the tunnel
```

`ReverseTunnelServer` implements `grpc.ServiceRegistrar`, so you use the exact same `RegisterXxxServer()` calls generated by protoc-gen-go-grpc. The service implementation is a normal gRPC server implementation -- no tunnel-specific code needed.

### Server-side code (control plane calling back to agents)

```go
// Server-side: use the tunnel as a grpc.ClientConnInterface
handler := grpctunnel.NewTunnelServiceHandler(opts)
tunnelpb.RegisterTunnelServiceServer(grpcServer, handler.Service())

// Later, to call an agent:
ch := handler.KeyAsChannel(clusterID)
ch.WaitForReady(ctx)
client := myservicepb.NewMyServiceClient(ch)
resp, err := client.DoSomething(ctx, req)
```

### The key insight

Both sides use standard gRPC interfaces:
- Agent implements services via `grpc.ServiceRegistrar` -- normal `RegisterXxxServer()` calls
- Control plane calls services via `grpc.ClientConnInterface` -- normal `NewXxxClient()` stubs
- No custom framing, no request ID correlation, no manual stream management

---

## 4. Keepalive / Reconnection

### Keepalive

**The library does NOT implement its own keepalive mechanism.** It relies on the underlying gRPC connection's keepalive configuration. Since the tunnel is just a gRPC bidi stream, you configure keepalive at the gRPC transport level:

```go
conn, _ := grpc.NewClient(addr,
    grpc.WithKeepaliveParams(keepalive.ClientParameters{
        Time:                30 * time.Second,
        Timeout:             10 * time.Second,
        PermitWithoutStream: true,
    }),
)
```

This is actually **better** than a custom heartbeat because gRPC keepalive operates at the HTTP/2 PING frame level, which is lower overhead and more reliable than application-level heartbeats.

### Reconnection

**The library does NOT handle reconnection.** `ReverseTunnelServer.Serve()` returns when the tunnel stream ends (either cleanly or due to error). The caller is responsible for retry logic.

This means you need a reconnect loop like your current code:

```go
for {
    started, err := revServer.Serve(ctx)
    if ctx.Err() != nil {
        return ctx.Err()
    }
    log.WithError(err).Warn("tunnel disconnected, reconnecting...")
    time.Sleep(backoff)
}
```

This is a reasonable design choice -- reconnection policy (backoff strategy, jitter, max retries) is application-specific.

---

## 5. Maturity Assessment

### Author credentials

Josh Humphries (`jhump`) is the co-founder and CTO of **Buf Technologies** (makers of buf, connect-go, the BSR). He is one of the most credible authors in the gRPC/protobuf ecosystem. His other major Go libraries include:
- `jhump/protoreflect` -- the de facto standard for protobuf reflection in Go
- `fullstorydev/grpchan` -- gRPC channel abstraction (a dependency of grpctunnel)
- `jhump/protocompile` -- pure Go protobuf compiler (powers buf)

### Repository metrics

| Metric | Value |
|---|---|
| Stars | 118 |
| Forks | 12 |
| Open issues | 1 (request to tag new version) |
| Closed issues | 20 (all resolved) |
| License | Apache 2.0 |
| Latest release | v0.3.0 (2023-10-30) |
| Latest commit | 2025-09-10 (dependency updates) |
| Go version | 1.24 |
| Contributors | 1 (jhump, plus dependabot) |
| Created | 2018-11-01 |

### Issue history (noteworthy)

- **#7, #8 (2023):** Deadlock issues with streaming-heavy usage -- **fixed** in v0.2.0/v0.3.0 with flow control
- **#9 (2023):** Concurrency test added, code hardened
- **#11 (2023):** Flow control implemented (protocol revision 1) -- the most significant engineering effort
- **#14, #15 (2023):** Backward compatibility bridge and test programs for cross-version testing
- **#20 (2025):** Updated to modern Go, refreshed dependencies
- **#21 (2026):** Open request to tag a new release (dependency updates are on main but untagged)

### Test coverage

The test suite covers:
- Forward tunnels (with and without flow control)
- Reverse tunnels (with and without flow control)
- **Nested/recursive tunnels** (tunnels over tunnels)
- Deadlock prevention (slow stream + 10 concurrent fast streams)
- Concurrency stress test (10 goroutines, 2 seconds continuous)
- Goroutine leak detection (checks goroutine count before/after)
- Cross-version compatibility test programs

### Verdict: solid but niche

**Strengths:**
- Written by an extremely credible author in the gRPC ecosystem
- Addresses real deadlock issues (flow control added after production feedback)
- Clean API that integrates with standard gRPC interfaces
- Good test coverage including edge cases
- Apache 2.0 license, actively maintained

**Weaknesses:**
- Pre-v1.0 (v0.3.0) -- API could theoretically change
- Single contributor (bus factor = 1, albeit a very reliable one)
- 118 stars suggests relatively small user base
- Not heavily battle-tested in public (likely used internally at Buf/FullStory)
- Latest tagged release is from 2023 (though main branch has 2025 updates)
- No built-in reconnection, keepalive, or observability

### Is it production-grade?

**Conditionally yes.** The code quality is high, the protocol design is thoughtful (flow control, protocol negotiation, backward compat), and the author is trustworthy. However:
- You should pin to a specific commit or fork if the untagged dependency updates matter
- You need to bring your own reconnection logic
- You need to bring your own observability (though gRPC interceptors work)
- The single-contributor risk is real, though the codebase is small enough (~2K lines) to maintain yourself if needed

---

## 6. Practical Example: Replacing the Existing Tunnel

### What the current code does

Your existing tunnel (`internal/tunnel/client.go`, ~315 lines) implements a custom HTTP-over-gRPC reverse tunnel:

1. Agent connects outbound to control plane via `ClusterTunnel.Connect` bidi stream
2. Server sends `HttpRequest` messages through the stream
3. Agent proxies each request to the local Kubernetes API server
4. Agent sends back `HttpResponseStart` + `HttpResponseBody` (chunked) + `HttpResponseEnd`
5. Manual request ID correlation for multiplexing
6. Custom heartbeat loop (30s interval)
7. Custom reconnection with exponential backoff
8. Thread-safe stream writer with mutex

### What grpctunnel replaces

| Current custom code | grpctunnel equivalent |
|---|---|
| `proxy.proto` (TunnelMessage, HttpRequest, etc.) | `tunnel.proto` (ClientToServer, ServerToClient, etc.) |
| Manual request ID correlation | Automatic stream_id multiplexing |
| `streamWriter` with mutex | Built-in thread-safe stream wrapper |
| 16KB chunking in `proxyRequest` | Built-in 16KB MessageData chunking |
| Custom heartbeat loop | gRPC transport keepalive |
| Manual `oneof` frame dispatch | Automatic gRPC method dispatch |

### What grpctunnel does NOT replace

| Still needed | Why |
|---|---|
| Reconnection loop | Library returns on disconnect |
| TLS/auth configuration | Application-specific |
| Health check endpoint | Application-specific |
| Kubernetes HTTP proxying logic | This is your business logic |

### The new design

Instead of tunneling raw HTTP bytes over a custom protocol, you would:

1. Define a proper gRPC service for the proxy (e.g., `KubeProxy`)
2. Implement it as a standard gRPC server on the agent
3. Register it with `ReverseTunnelServer`
4. The control plane calls it via `handler.KeyAsChannel(clusterID)`

### Step-by-step agent code

#### A. Define a proper gRPC proxy service

```protobuf
syntax = "proto3";
package castai.cloud.proxy.v1alpha1;
option go_package = "github.com/castai/cluster-controller/internal/tunnel/pb";

service KubeProxy {
  rpc ProxyHTTP(HttpRequest) returns (stream HttpResponseChunk);
}

message HttpRequest {
  string method = 1;
  string path = 2;
  repeated Header headers = 3;
  bytes body = 4;
}

message HttpResponseChunk {
  oneof payload {
    HttpResponseStart start = 1;
    bytes body = 2;
  }
}

message HttpResponseStart {
  int32 status_code = 1;
  repeated Header headers = 2;
}

message Header {
  string key = 1;
  string value = 2;
}
```

Note: no `request_id` needed -- each gRPC call is its own stream with automatic multiplexing.

#### B. Implement the service (agent side)

```go
package tunnel

import (
    "context"
    "fmt"
    "io"
    "net/http"

    "github.com/castai/cluster-controller/internal/tunnel/pb"
)

const streamChunkSize = 32 * 1024

type kubeProxyServer struct {
    pb.UnimplementedKubeProxyServer
    httpClient *http.Client
    kubeURL    string
}

func (s *kubeProxyServer) ProxyHTTP(req *pb.HttpRequest, stream pb.KubeProxy_ProxyHTTPServer) error {
    httpReq, err := http.NewRequestWithContext(stream.Context(), req.Method, s.kubeURL+req.Path, nil)
    if err != nil {
        return fmt.Errorf("creating request: %w", err)
    }

    if len(req.Body) > 0 {
        httpReq.Body = io.NopCloser(bytes.NewReader(req.Body))
        httpReq.ContentLength = int64(len(req.Body))
    }

    for _, h := range req.Headers {
        httpReq.Header.Add(h.Key, h.Value)
    }

    resp, err := s.httpClient.Do(httpReq)
    if err != nil {
        return fmt.Errorf("executing request: %w", err)
    }
    defer resp.Body.Close()

    var headers []*pb.Header
    for k, vals := range resp.Header {
        for _, v := range vals {
            headers = append(headers, &pb.Header{Key: k, Value: v})
        }
    }

    if err := stream.Send(&pb.HttpResponseChunk{
        Payload: &pb.HttpResponseChunk_Start{
            Start: &pb.HttpResponseStart{
                StatusCode: int32(resp.StatusCode),
                Headers:    headers,
            },
        },
    }); err != nil {
        return err
    }

    buf := make([]byte, streamChunkSize)
    for {
        n, readErr := resp.Body.Read(buf)
        if n > 0 {
            if err := stream.Send(&pb.HttpResponseChunk{
                Payload: &pb.HttpResponseChunk_Body{Body: buf[:n]},
            }); err != nil {
                return err
            }
        }
        if readErr == io.EOF {
            return nil
        }
        if readErr != nil {
            return fmt.Errorf("reading response body: %w", readErr)
        }
    }
}
```

This is a **completely standard gRPC server-streaming implementation**. No tunnel awareness whatsoever.

#### C. Wire it up with grpctunnel (agent main)

```go
package main

import (
    "context"
    "crypto/tls"
    "net/http"
    "time"

    "github.com/jhump/grpctunnel"
    "github.com/jhump/grpctunnel/tunnelpb"
    "github.com/sirupsen/logrus"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/keepalive"
    "google.golang.org/grpc/metadata"

    "github.com/castai/cluster-controller/internal/tunnel/pb"
    "github.com/castai/cluster-controller/internal/waitext"
)

func runAgent(ctx context.Context, log logrus.FieldLogger, cfg Config, httpClient *http.Client) error {
    conn, err := grpc.NewClient(cfg.Address,
        grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                30 * time.Second,
            Timeout:             10 * time.Second,
            PermitWithoutStream: true,
        }),
    )
    if err != nil {
        return fmt.Errorf("dialing gRPC server: %w", err)
    }
    defer conn.Close()

    tunnelStub := tunnelpb.NewTunnelServiceClient(conn)
    revServer := grpctunnel.NewReverseTunnelServer(tunnelStub)

    pb.RegisterKubeProxyServer(revServer, &kubeProxyServer{
        httpClient: httpClient,
        kubeURL:    "https://kubernetes.default.svc",
    })

    md := metadata.New(map[string]string{
        "x-cluster-id": cfg.ClusterID,
    })
    streamCtx := metadata.NewOutgoingContext(ctx, md)

    // Reconnection loop (grpctunnel does not handle this)
    for {
        log.Info("opening reverse tunnel")
        _, err := revServer.Serve(streamCtx)
        if ctx.Err() != nil {
            return ctx.Err()
        }

        log.WithError(err).Warn("tunnel disconnected, reconnecting")
        boff := waitext.DefaultExponentialBackoff()
        waitext.Retry(ctx, boff, 1, func(ctx context.Context) (bool, error) {
            return false, nil
        }, nil)
    }
}
```

#### D. Control plane side (server calling the agent)

```go
// On the control plane server:
handler := grpctunnel.NewTunnelServiceHandler(grpctunnel.TunnelServiceHandlerOptions{
    AffinityKey: func(ch grpctunnel.TunnelChannel) any {
        md, _ := grpctunnel.TunnelMetadataFromIncomingContext(ch.Context())
        if vals := md.Get("x-cluster-id"); len(vals) > 0 {
            return vals[0]
        }
        return ""
    },
})

// Register the tunnel service with the gRPC server
tunnelpb.RegisterTunnelServiceServer(grpcServer, handler.Service())

// Later, to proxy a request to a specific cluster's agent:
func proxyToCluster(ctx context.Context, handler *grpctunnel.TunnelServiceHandler, clusterID string, req *pb.HttpRequest) error {
    ch := handler.KeyAsChannel(clusterID)
    if err := ch.WaitForReady(ctx); err != nil {
        return fmt.Errorf("waiting for tunnel: %w", err)
    }

    client := pb.NewKubeProxyClient(ch)
    stream, err := client.ProxyHTTP(ctx, req)
    if err != nil {
        return err
    }

    for {
        chunk, err := stream.Recv()
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }
        // Process chunk.GetStart() or chunk.GetBody()
    }
}
```

---

## 7. Line Count Comparison

| Component | Current | With grpctunnel |
|---|---|---|
| Proto definition | 43 lines (proxy.proto) | ~30 lines (KubeProxy service) |
| Tunnel client + stream handling | 315 lines (client.go) | ~60 lines (reconnect loop + setup) |
| HTTP proxy logic | embedded in client.go | ~60 lines (standalone gRPC service) |
| Stream writer / mutex | embedded in client.go | 0 (handled by library) |
| Heartbeat loop | embedded in client.go | 0 (gRPC keepalive) |
| Request ID correlation | embedded in client.go | 0 (automatic stream multiplexing) |
| **Total agent code** | **~315 lines** | **~120 lines** |
| **Reduction** | | **~60% less code** |

The proxy logic itself stays roughly the same size since it is business logic. What disappears entirely is:
- The `streamWriter` mutex wrapper
- The heartbeat goroutine
- The manual `request_id` correlation across start/body/end messages
- The `handleStream` dispatch loop
- The `sendErrorResponse` helper (gRPC errors replace it)

---

## 8. Risks and Considerations

### Migration risks

1. **Protocol break:** The wire protocol is completely different. Both agent and server must be updated simultaneously, or you need a migration period with dual support.

2. **Server-side changes required:** The control plane server also needs to adopt `grpctunnel.TunnelServiceHandler`. This is not an agent-only change.

3. **Error semantics change:** Currently errors are encoded as HTTP 502 responses. With gRPC, errors become gRPC status codes. The control plane needs to handle this differently.

4. **Request body streaming:** The current proto sends the entire request body in one message. If you need streaming request bodies (e.g., large file uploads), you'd need a client-streaming or bidi-streaming proxy RPC instead of unary-request/server-streaming-response.

### What to watch

1. **No tagged release since 2023-10:** Main branch has dependency updates from 2025 but no new tag. Issue #21 requests this. You may want to reference a commit hash in `go.mod` rather than the v0.3.0 tag.

2. **Single contributor:** If Josh Humphries stops maintaining it, you own it. The codebase is ~2K lines of Go, so this is manageable but worth noting.

3. **grpchan dependency:** The library depends on `github.com/fullstorydev/grpchan` (also by jhump) for the channel abstraction. This adds a transitive dependency.

---

## 9. Recommendation

**The library is a strong fit for replacing the current custom tunnel.** The reverse tunnel capability directly matches the use case, the API is clean, and it eliminates the most error-prone parts of the current code (multiplexing, flow control, heartbeat, thread safety).

The main trade-off is taking a dependency on a niche library with a single maintainer vs. maintaining ~200 lines of custom tunnel plumbing. Given that the custom code has already needed careful engineering (mutex-protected stream writer, chunked responses, request ID correlation), and the library addresses known hard problems (flow control deadlocks) that the current code does not handle, the dependency is justified.

**Next steps if proceeding:**
1. Validate that the control plane server team is willing to adopt `TunnelServiceHandler`
2. Pin to a specific commit hash on main (for the 2025 dependency updates) or wait for a new tagged release
3. Define the `KubeProxy` gRPC service
4. Implement the agent side (~120 lines)
5. Implement the server side (adopts `TunnelServiceHandler` + `KeyAsChannel`)
6. Plan a coordinated rollout (both sides must change)

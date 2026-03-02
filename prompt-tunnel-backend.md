# Task: Implement gRPC ClusterTunnel Server

## Context

We have a **tunnel client** deployed as a sidecar in customer Kubernetes clusters (inside `castai-cluster-controller`). The client opens an outbound gRPC bidirectional stream to our backend and waits for instructions.

Your job is to implement the **backend server side** — a gRPC service that:
1. Accepts incoming tunnel connections from cluster-controller sidecars
2. Authenticates them via gRPC metadata
3. Maintains a registry of connected clusters
4. Exposes an internal API (HTTP or gRPC) that other backend services (e.g. kubernetes-mcp-server) can call to send Kubernetes API requests through the tunnel to a specific cluster

## Proto Contract

Copy this proto file into the backend repo and generate server code from it.

```protobuf
syntax = "proto3";
package castai.cloud.proxy.v1alpha1;
option go_package = "<your-backend-go-package>/internal/tunnel/pb";

service ClusterTunnel {
  rpc Connect(stream TunnelMessage) returns (stream TunnelMessage);
}

message TunnelMessage {
  oneof payload {
    HttpRequest  http_request  = 1;
    HttpResponse http_response = 2;
    Heartbeat    heartbeat     = 3;
  }
}

message HttpRequest {
  string request_id = 1;
  string method = 2;
  string path = 3;
  repeated Header headers = 4;
  bytes body = 5;
}

message HttpResponse {
  string request_id = 1;
  int32 status_code = 2;
  repeated Header headers = 3;
  bytes body = 4;
  string error = 5;
}

message Heartbeat { int64 timestamp_ms = 1; }
message Header { string key = 1; repeated string values = 2; }
```

## Client Behavior (already implemented — do not change)

This is what the tunnel client does. The server must be compatible with this behavior.

### Connection

- Client calls `ClusterTunnel/Connect` to open a bidirectional stream
- Client sends two gRPC metadata headers with every `Connect` call:
  - `x-cluster-id` — the cluster UUID
  - `x-api-key` — the CAST AI API key for authentication
- Client reconnects with exponential backoff (1s initial, 1.5x multiplier, 60s max) on any stream error
- Client uses TLS when `TLS_CA_CERT` is provided, otherwise insecure (dev only)

### Messages client sends (server → receives)

1. **Heartbeat** — sent every 30 seconds with `timestamp_ms` (Unix millis). The server should use this to detect stale connections.
2. **HttpResponse** — sent in response to an `HttpRequest` from the server. Correlated by `request_id`.

### Messages client receives (server → sends)

1. **HttpRequest** — the server sends this when it wants to make a Kubernetes API call through the tunnel. Fields:
   - `request_id` — unique ID (UUID recommended). The client echoes this back in the response.
   - `method` — HTTP method (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`)
   - `path` — Kubernetes API path, e.g. `/api/v1/namespaces`, `/apis/apps/v1/deployments`
   - `headers` — HTTP headers to forward (multi-value supported via repeated `values`)
   - `body` — request body bytes (for POST/PUT/PATCH)

### Response behavior

- The client proxies the request to `https://kubernetes.default.svc{path}` using the pod's ServiceAccount token
- The response comes back as an `HttpResponse` with the same `request_id`
- On proxy failure, `HttpResponse.error` is set and `status_code` is `502`
- Max response body size: **10 MB**
- Client handles multiple concurrent requests on the same stream (responses may arrive out of order)

## What to Implement

### 1. gRPC Server — `ClusterTunnel/Connect` handler

**Authentication:**
- Extract `x-cluster-id` and `x-api-key` from incoming gRPC metadata
- Validate the API key (check it belongs to the cluster, is not revoked, etc.)
- Reject with `codes.Unauthenticated` if invalid

**Connection registry:**
- On successful auth, register the stream in an in-memory map: `clusterID → stream`
- On stream close (client disconnect, error, context cancel), remove from the map
- Handle the case where a cluster reconnects (replace the old stream entry)
- Periodically check heartbeat timestamps and consider a connection stale if no heartbeat for >90 seconds

**Stream loop:**
- Read messages from the client stream in a loop
- Handle `HttpResponse` messages: look up the pending request by `request_id`, deliver the response to the waiting caller
- Handle `Heartbeat` messages: update the last-seen timestamp for the cluster
- The loop runs until the stream is closed or context is cancelled

### 2. Pending Request Tracking

When the server sends an `HttpRequest`, it needs to wait for the matching `HttpResponse`:

```
type pendingRequest struct {
    responseCh chan *pb.HttpResponse
}
```

- Map of `request_id → pendingRequest`
- When sending a request: generate UUID, create channel, store in map, send `HttpRequest` on stream, wait on channel with timeout
- When receiving a response: look up `request_id` in map, send response on channel, delete from map
- Timeout: 30 seconds (configurable). Return `504 Gateway Timeout` equivalent if no response.
- Clean up: if the stream dies, close all pending request channels with an error

### 3. Internal API for Other Services

Expose a way for other backend services to send Kubernetes API requests through the tunnel. This could be:

**Option A: Internal gRPC service**
```protobuf
service KubeProxy {
  rpc ProxyRequest(KubeProxyRequest) returns (KubeProxyResponse);
}

message KubeProxyRequest {
  string cluster_id = 1;
  string method = 2;
  string path = 3;
  repeated Header headers = 4;
  bytes body = 5;
}

message KubeProxyResponse {
  int32 status_code = 1;
  repeated Header headers = 2;
  bytes body = 3;
  string error = 4;
}
```

**Option B: Internal HTTP endpoint**
```
POST /internal/v1/clusters/{cluster_id}/kube-proxy
X-Kube-Method: GET
X-Kube-Path: /api/v1/namespaces

→ proxied response body, status code, headers
```

Either way, the flow is:
1. Caller provides `cluster_id` + HTTP request details
2. Server looks up the cluster's active stream in the registry
3. If not connected → return error (e.g. `503 Cluster Not Connected`)
4. Generate `request_id`, send `HttpRequest` on the stream
5. Wait for matching `HttpResponse` (with timeout)
6. Return the response to the caller

### 4. Send concurrency safety

gRPC `stream.Send()` is **not goroutine-safe**. If multiple internal callers want to send requests to the same cluster concurrently, the sends must be serialized. Use a mutex around `Send`, same pattern as the client:

```go
type streamWriter struct {
    mu     sync.Mutex
    stream pb.ClusterTunnel_ConnectServer
}

func (sw *streamWriter) Send(msg *pb.TunnelMessage) error {
    sw.mu.Lock()
    defer sw.mu.Unlock()
    return sw.stream.Send(msg)
}
```

## Scope Limitations (v1)

- **Request-response only** — no SPDY/WebSocket upgrade (no `kubectl exec/attach/port-forward`)
- **No streaming responses** — single `HttpResponse` per `HttpRequest` (no `kubectl logs -f`)
- **Max 10 MB** response body per request
- **Single stream per cluster** — if a cluster reconnects, replace the old stream

## Test Scenarios

1. **Basic round-trip**: Connect a mock client, send `HttpRequest` for `GET /api/v1/namespaces` on the stream, verify client sends back `HttpResponse` with status 200
2. **Authentication**: Connect without `x-api-key` metadata → should get `Unauthenticated`
3. **Cluster not connected**: Call internal API for a cluster that has no active stream → should get `503`
4. **Concurrent requests**: Send 10 requests on the same stream simultaneously, verify all 10 responses arrive with correct `request_id` correlation
5. **Request timeout**: Send a request, mock client never responds → should timeout after 30s and return error
6. **Reconnection**: Client disconnects and reconnects → new stream should replace old one, pending requests on old stream should fail
7. **Heartbeat staleness**: Client connects but sends no heartbeats for >90s → connection should be considered stale

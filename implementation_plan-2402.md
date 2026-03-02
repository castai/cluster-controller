# Plan: gRPC Reverse Tunnel — Separate Binary (Sidecar Pattern)

## Context

The kubernetes-mcp-server (and developer kubectl) need to reach customer kube-apiservers through the backend. We add a new binary `castai-tunnel` that runs as a sidecar container alongside the cluster-controller. It opens an outbound gRPC bidirectional stream to the backend, receives K8s API HTTP requests, proxies them to `https://kubernetes.default.svc`, and returns the response.

Follows the exact same pattern as the existing kubectl sidecar (`cmd/sidecar/`, `Dockerfile.sidecar`).

## Scope (v1)

- Request-response only (no SPDY/WebSocket — no `kubectl exec/attach`)
- Single message per response (max 10MB body)
- No streaming (`kubectl logs -f` not supported)

---

## New Files

### 1. `proto/castai/cloud/proxy/v1alpha1/proxy.proto`

```protobuf
syntax = "proto3";
package castai.cloud.proxy.v1alpha1;
option go_package = "github.com/castai/cluster-controller/internal/tunnel/pb";

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

### 2. `internal/tunnel/pb/` — generated Go code

Auto-generated `proxy.pb.go` and `proxy_grpc.pb.go`. Via new Makefile target.

### 3. `internal/tunnel/client.go` — tunnel gRPC client

The core component. Connects to backend, receives requests, proxies to kube-apiserver.

```
type Config struct {
    Address            string
    ClusterID          string
    APIKey             string
    TLSCACert          string
    HeartbeatInterval  time.Duration
    MaxRequestBodySize int
}

type Client struct {
    log        logrus.FieldLogger
    cfg        Config
    httpClient *http.Client    // built from rest.TransportFor(restConfig) for kube auth
    mu         sync.Mutex
    connected  bool
}
```

Key methods:
- `NewClient(log, cfg, restConfig)` — creates kube HTTP transport via `rest.TransportFor(restConfig)`
- `Run(ctx)` — reconnect loop using `waitext.DefaultExponentialBackoff()`, blocks until ctx done
- `connect(ctx)` — dials gRPC with TLS, sends cluster_id + api_key as gRPC metadata, opens stream
- `handleStream(ctx, stream)` — receive loop: dispatches HttpRequest to goroutines, heartbeat sender goroutine
- `proxyRequest(ctx, *pb.HttpRequest) *pb.HttpResponse` — converts proto → `http.Request` targeting `https://kubernetes.default.svc{path}`, calls `httpClient.Do()`, converts response to proto
- `streamWriter` — wraps `stream.Send()` with mutex (gRPC Send is not goroutine-safe)
- `IsConnected() bool`, `Close()`

Reuses:
- `internal/waitext` for retry/backoff (same patterns as `internal/controller/controller.go:105-116`)
- `k8s.io/client-go/rest.TransportFor()` to get an HTTP transport with in-cluster service account token + CA
- TLS config creation duplicated locally (small function, avoids coupling to `internal/castai`)

### 4. `internal/tunnel/client_test.go`

Tests using `google.golang.org/grpc/test/bufconn` for in-process gRPC:
- proxyRequest round-trip with `httptest.NewServer` as fake kube-apiserver
- Concurrent requests with request_id correlation
- Multi-value header preservation
- Reconnection on stream error
- Context cancellation and graceful drain

### 5. `cmd/tunnel/main.go` — entrypoint

Mirrors `cmd/sidecar/main.go`:

```go
func main() {
    ctx := signals.SetupSignalHandler()
    if err := newCommand().ExecuteContext(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

### 6. `cmd/tunnel/command.go` — cobra command + wiring

Mirrors `cmd/sidecar/command.go`. Reads config from env vars, creates components, runs.

Env vars (all required):
- `API_URL` — backend URL (used to derive gRPC address)
- `API_KEY` — auth
- `CLUSTER_ID` — cluster identifier
- `TLS_CA_CERT` — optional custom CA

Env vars (optional with defaults):
- `TUNNEL_ADDRESS` — gRPC server address (default: derived from API_URL)
- `TUNNEL_HEARTBEAT_INTERVAL` — heartbeat interval (default: 30s)
- `TUNNEL_MAX_REQUEST_BODY_SIZE` — max response body (default: 10MB)
- `TUNNEL_HEALTH_PORT` — health check HTTP port (default: 8091)

Wiring in `run(ctx)`:
1. Load config from env vars
2. Get kube rest.Config (`rest.InClusterConfig()` or from KUBECONFIG)
3. Create `tunnel.Client`
4. Start health HTTP server (`:8091/healthz`)
5. `tunnelClient.Run(ctx)` — blocks

### 7. `Dockerfile.tunnel`

Minimal, no kubectl needed (unlike sidecar):

```dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY bin/castai-tunnel-$TARGETARCH /usr/local/bin/castai-tunnel
CMD ["castai-tunnel"]
```

---

## Modified Files

### 8. `Makefile`

Add:

```makefile
TUNNEL_DOCKER_REPOSITORY ?= us-docker.pkg.dev/castai-hub/library/cluster-controller-tunnel

build-tunnel:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -ldflags "-s -w" -o bin/castai-tunnel-$(ARCH) ./cmd/tunnel
	docker build --platform=linux/$(ARCH) --build-arg TARGETARCH=$(ARCH) -f Dockerfile.tunnel -t $(TUNNEL_DOCKER_REPOSITORY):$(VERSION) .

generate-proto:
	protoc \
		--go_out=internal/tunnel/pb --go_opt=paths=source_relative \
		--go-grpc_out=internal/tunnel/pb --go-grpc_opt=paths=source_relative \
		proto/castai/cloud/proxy/v1alpha1/proxy.proto
```

### 9. `go.mod`

Promote from indirect to direct:
- `google.golang.org/grpc`
- `google.golang.org/protobuf`

---

## Files NOT modified

- `cmd/controller/run.go` — no changes, tunnel is independent
- `internal/config/config.go` — tunnel has its own config in `cmd/tunnel/command.go`
- `internal/metrics/` — tunnel has its own lightweight metrics (can add later if needed)

---

## Implementation Order

1. Proto definition + `generate-proto` Makefile target + run codegen
2. `internal/tunnel/client.go` + `internal/tunnel/client_test.go`
3. `cmd/tunnel/main.go` + `cmd/tunnel/command.go`
4. `Dockerfile.tunnel` + `build-tunnel` Makefile target
5. `go.mod` tidy

## Verification

```bash
# Proto generation
make generate-proto

# Build
go build ./cmd/tunnel/...

# Tests
go test ./internal/tunnel/... -race

# Full build with Docker image
make build-tunnel VERSION=dev ARCH=amd64

# Integration: run a test gRPC server, set TUNNEL_ADDRESS to it,
# have it send GET /api/v1/namespaces, verify JSON response
```

# Konnectivity Client Library: Deep Technical Analysis

**Date:** 2026-03-02
**Purpose:** Evaluate whether `sigs.k8s.io/apiserver-network-proxy/konnectivity-client` can be used independently of the Kubernetes API server to build a reverse tunnel from a customer cluster agent to our backend.

---

## Executive Summary

**Verdict: The konnectivity-client library is NOT suitable for our use case.**

The library is designed exclusively for the **frontend side** (API server / caller side) of the Konnectivity tunnel. It does not contain agent-side code. The agent implementation lives in the main `sigs.k8s.io/apiserver-network-proxy` module, which pulls in the full Kubernetes dependency tree (`k8s.io/client-go`, `k8s.io/apimachinery`, `controller-runtime`, etc.). There is no clean way to use just the agent-side tunnel establishment without importing the heavy main module.

More critically, the architecture is a **poor fit**: Konnectivity's tunnel direction is inverted relative to our needs, the client library is single-use-per-connection (no multiplexing), and the proxy server is a mandatory middle component that adds deployment complexity with no benefit for our scenario.

---

## 1. Architecture Overview

Konnectivity has three components:

```
+----------------+       gRPC stream        +----------------+       TCP dial        +----------------+
|  konnectivity  | <----------------------> |  konnectivity  | --------------------> |   target       |
|    client       |   (ProxyService.Proxy)  |    server       |   (net.DialTimeout)  |   service      |
|  (API server)  |                          |  (proxy)        |                      |  (in cluster)  |
+----------------+                          +----------------+                      +----------------+
                                                  ^
                                                  | gRPC stream
                                                  | (AgentService.Connect)
                                                  |
                                           +----------------+
                                           |  konnectivity  |
                                           |    agent        |
                                           |  (in cluster)  |
                                           +----------------+
```

**Data flow for a single request:**
1. Client (API server) creates a `Tunnel` via `CreateSingleUseGrpcTunnelWithContext()`.
2. Client calls `tunnel.DialContext("tcp", "10.0.0.5:6443")` -- this sends a `DIAL_REQ` packet to the proxy server.
3. Proxy server forwards the `DIAL_REQ` to an agent that has a gRPC stream open.
4. Agent receives the `DIAL_REQ`, calls `net.DialTimeout(protocol, address, 5s)` to the actual target, and sends back a `DIAL_RSP`.
5. Bidirectional `DATA` packets flow between client and agent, relayed through the proxy server.
6. `CLOSE_REQ`/`CLOSE_RSP` teardown.

---

## 2. What Does the `konnectivity-client` Package Actually Contain?

The `konnectivity-client` module (`sigs.k8s.io/apiserver-network-proxy/konnectivity-client`) is a **separate Go module** with its own `go.mod`. It contains:

### Packages:
- `pkg/client/` -- The `Tunnel` interface and `grpcTunnel` implementation
- `pkg/client/metrics/` -- Prometheus metrics for the client side
- `pkg/common/metrics/` -- Shared metric types
- `proto/client/` -- Protobuf definitions for the wire protocol

### Public API:
```go
// The only public interface
type Tunnel interface {
    DialContext(ctx context.Context, protocol, address string) (net.Conn, error)
    Done() <-chan struct{}
}

// Factory functions
func CreateSingleUseGrpcTunnel(tunnelCtx context.Context, address string, opts ...grpc.DialOption) (Tunnel, error)
func CreateSingleUseGrpcTunnelWithContext(createCtx, tunnelCtx context.Context, address string, opts ...grpc.DialOption) (Tunnel, error)
```

### Critical Limitation -- Single-Use Tunnel:
```go
// From the source code:
// "Currently, a single tunnel supports a single connection."
// "The Dial() method of the returned tunnel should only be called once"

func (t *grpcTunnel) dialContext(...) (net.Conn, error) {
    prevStarted := atomic.SwapUint32(&t.started, 1)
    if prevStarted != 0 {
        return nil, &dialFailure{"single-use dialer already dialed", ...}
    }
    // ...
}
```

Each `Tunnel` instance supports **exactly one** `DialContext()` call. To make N concurrent requests, you need N tunnels, each with its own gRPC stream to the proxy server. There is no connection multiplexing at the tunnel level.

---

## 3. Where Does the Agent Code Live?

The agent implementation is in the **main module** (`sigs.k8s.io/apiserver-network-proxy`), NOT in the client module:

- `pkg/agent/client.go` -- Core `Client` struct, gRPC stream management, packet routing
- `pkg/agent/clientset.go` -- `ClientSet` managing multiple connections with reconnection logic
- `cmd/agent/` -- CLI entry point with Cobra command

### Agent's `go.mod` dependencies (main module):
```
module sigs.k8s.io/apiserver-network-proxy
go 1.24.7

require (
    k8s.io/api
    k8s.io/apimachinery
    k8s.io/client-go
    k8s.io/component-base
    sigs.k8s.io/controller-runtime
    github.com/spf13/cobra
    google.golang.org/grpc
    // ... 25+ direct dependencies, 100+ transitive
)
```

**This is the heavy Kubernetes module.** Importing anything from `pkg/agent/` pulls in the entire Kubernetes client ecosystem.

### Agent Options that Reveal Kubernetes Coupling:
- `CountServerLeases` -- uses Kubernetes Lease objects to discover proxy server count
- `LeaseNamespace` (default: `kube-system`)
- `LeaseLabel` (default: `k8s-app=konnectivity-server`)
- `ServiceAccountTokenPath` -- expects Kubernetes projected service account tokens
- `KubeconfigPath` -- optional kubeconfig for lease watching
- `AgentIdentifiers` -- URL-encoded identifiers (IPv4, IPv6, CIDR, Host, DefaultRoute)

---

## 4. Detailed Analysis of Each Question

### Q1: Can konnectivity-client be used as a standalone library?

**Partially yes, but it is the wrong component.** The `konnectivity-client` module has minimal dependencies:

```
Direct dependencies:
- github.com/prometheus/client_golang v1.11.1
- google.golang.org/grpc v1.56.3
- google.golang.org/protobuf v1.33.0
- k8s.io/klog/v2 v2.0.0
```

No `k8s.io/client-go`, no `k8s.io/apimachinery`. **The dependency footprint is clean.** However, this is the **caller/frontend** side -- it is what the Kubernetes API server uses to send requests through the tunnel. It is NOT the agent that runs inside the cluster.

For our use case (agent in customer cluster, backend sends requests through tunnel), we need to act as both:
- The **agent** (in the customer cluster) -- this is the heavy module
- The **client/frontend** (in our backend) -- this is the light module

And we also need the **proxy server** as the middle component.

### Q2: What does the agent-side code look like?

The agent code is substantial (~500 lines in `client.go` + ~200 lines in `clientset.go`). Key parts:

**Connection establishment:**
```go
func (a *Client) Connect() (int, error) {
    conn, err := grpc.Dial(a.address, a.opts...)
    ctx := metadata.AppendToOutgoingContext(context.Background(),
        header.AgentID, a.agentID,
        header.AgentIdentifiers, a.agentIdentifiers)
    stream, err := agent.NewAgentServiceClient(conn).Connect(ctx)
    serverID, err := serverID(stream)  // reads from gRPC headers
    serverCount, err := serverCount(stream)  // reads from gRPC headers
    // ...
}
```

**The serve loop (packet routing):**
```go
func (a *Client) Serve() {
    for {
        pkt, err := a.Recv()
        switch pkt.Type {
        case client.PacketType_DIAL_REQ:
            // net.DialTimeout to target, bidirectional relay goroutines
        case client.PacketType_DATA:
            // forward data to endpoint connection
        case client.PacketType_CLOSE_REQ:
            // cleanup connection
        }
    }
}
```

**Bidirectional relay:**
- `remoteToProxy()` -- reads from local TCP conn, sends DATA packets upstream
- `proxyToRemote()` -- reads from gRPC data channel, writes to local TCP conn

This is NOT "a few lines." It is a full connection multiplexer with goroutine management, cleanup logic, pprof labels, and metrics.

### Q3: Reconnection, keepalive, multiplexing, streaming?

| Feature | Status | Details |
|---------|--------|---------|
| **Reconnection** | Yes, in `ClientSet` | Exponential backoff with jitter (factor 1.5, jitter 0.1), configurable cap. Agent exits on unhealthy connection, `ClientSet` reconnects. |
| **Keepalive** | Partial | gRPC-level keepalive only (`KeepaliveTime` default: 1 hour). No application-level heartbeat. `probe()` checks `conn.GetState()` at `probeInterval` (default: 1s). |
| **Multiplexing** | Yes, agent-side | A single agent gRPC stream multiplexes many connections via `connectionManager` (map of connID -> endpointConn). |
| **Multiplexing** | NO, client-side | `konnectivity-client`'s `Tunnel` is single-use. One tunnel = one connection. |
| **Streaming HTTP** | Raw TCP only | The tunnel operates at TCP level. It passes raw bytes. HTTP streaming works because it is just TCP bytes flowing through. No HTTP-level awareness. |
| **Flow control** | Missing | Source code has `// TODO: flow control` comments. Channel buffer size is configurable (`XfrChannelSize`, default 150 packets). |

### Q4: Dependency footprint

**`konnectivity-client` module (the clean one):**
```
Direct: grpc, protobuf, prometheus, klog
Transitive: ~15 packages
No k8s.io/client-go, no k8s.io/apimachinery
```

**Main module (where agent lives):**
```
Direct: k8s.io/api, k8s.io/apimachinery, k8s.io/client-go,
        k8s.io/component-base, controller-runtime, cobra, grpc, ...
Transitive: 100+ packages (the full Kubernetes SDK)
```

Importing `pkg/agent/` from the main module would infect your binary with the entire Kubernetes client dependency tree.

### Q5: Clean separation between tunnel transport and Kubernetes logic?

**No.** The separation is:
- `konnectivity-client` = clean tunnel **client** (frontend/caller side only)
- Main module = agent + server + Kubernetes-specific logic, all mixed together

The agent code in `pkg/agent/` directly imports:
- `proto/header` (gRPC metadata keys like `AgentID`, `ServerID`, `ServerCount`)
- `proto/agent` (the `AgentService` gRPC service)
- `pkg/agent/metrics` (Prometheus metrics)
- Kubernetes lease watching for server discovery
- Service account token loading

There is no `pkg/agent/transport` or similar clean sub-package you could import independently.

---

## 5. Feasibility of Extraction

Could we extract just the tunnel protocol and build our own agent?

### What we would need to reimplement:
1. The `AgentService.Connect` gRPC client (~50 lines)
2. The packet routing loop from `Client.Serve()` (~150 lines)
3. The bidirectional relay (`remoteToProxy` + `proxyToRemote`) (~80 lines)
4. Connection management (`connectionManager`) (~40 lines)
5. The proto definitions (already in `konnectivity-client` module)

### What we would also need:
- **The proxy server binary** (`cmd/server/`) -- this is the mandatory middle component. Without it, there is no tunnel. The proxy server is ~2000 lines of code in the main module.

### The fundamental architectural mismatch:

Our desired architecture:
```
Agent (in customer cluster) --reverse tunnel--> Our Backend (sends HTTP requests)
```

Konnectivity's architecture:
```
Client (API server) --> Proxy Server --> Agent (in cluster) --> Target (in cluster)
```

In Konnectivity:
- The **agent** initiates the outbound connection (good, matches our reverse tunnel need)
- But the **proxy server** is a mandatory middle component (adds complexity)
- The **client** sends requests through the proxy server, not directly to the agent
- Request direction: client -> proxy server -> agent -> target (three hops)

For our use case, we would need:
- Deploy the Konnectivity proxy server in our backend infrastructure
- Deploy the Konnectivity agent in the customer cluster
- Use the `konnectivity-client` library in our backend to create tunnels through the proxy server
- Each HTTP request requires a new single-use tunnel (no multiplexing on the client side)

---

## 6. Protocol Analysis

The wire protocol is simple and well-defined:

```protobuf
service ProxyService {
  rpc Proxy(stream Packet) returns (stream Packet) {}
}

service AgentService {
  rpc Connect(stream Packet) returns (stream Packet) {}
}

enum PacketType {
  DIAL_REQ = 0;   // Client -> Agent: open a TCP connection to address
  DIAL_RSP = 1;   // Agent -> Client: connection established (or error)
  CLOSE_REQ = 2;  // Either direction: request to close
  CLOSE_RSP = 3;  // Either direction: acknowledge close
  DATA = 4;       // Either direction: raw bytes
  DIAL_CLS = 5;   // Cancel a pending dial
  DRAIN = 6;      // Agent -> Server: drain signal
}
```

The protocol itself is ~50 lines of protobuf. It is clean, simple, and reusable. But using it requires either:
1. Running the full Konnectivity proxy server, OR
2. Reimplementing the proxy server's routing logic

---

## 7. Alternatives Assessment

### Option A: Use Konnectivity as-is
- **Pros:** Battle-tested in Kubernetes production, handles reconnection
- **Cons:** Three-component deployment, no client-side multiplexing, heavy agent binary, proxy server is a mandatory extra component, architectural mismatch with our needs

### Option B: Extract the protocol, build custom agent + server
- **Pros:** Clean protocol, can reuse proto definitions from `konnectivity-client`
- **Cons:** Reimplementing ~500 lines of agent code + ~2000 lines of server code. At that point, why not use a purpose-built tunneling library?

### Option C: Use a purpose-built reverse tunneling solution
Better-suited alternatives for "agent in customer cluster, backend sends HTTP through tunnel":

| Solution | Description | Multiplexing | Reconnection | Dependency Weight |
|----------|-------------|--------------|--------------|-------------------|
| **chisel** | TCP tunnel over WebSocket, client/server model | Yes (via SSH channel mux) | Yes | Light (~10 deps) |
| **inlets** | Reverse tunnel with HTTP/TCP support | Yes | Yes | Medium |
| **frp** | Fast reverse proxy with multiplexing | Yes | Yes | Medium |
| **yamux + custom** | Pure multiplexed stream library | Yes (native) | Build your own | Minimal (~3 deps) |
| **gRPC bidirectional streaming (custom)** | Roll your own with the same gRPC pattern | Yes | Yes (with keepalive) | Light (grpc only) |

### Option D: Build a minimal tunnel using gRPC bidirectional streaming
The Konnectivity protocol is actually simple enough that building a purpose-fit version would be straightforward:

```go
// ~100 lines for a minimal agent
// ~100 lines for a minimal server-side handler
// Reuse gRPC for transport, TLS, keepalive, reconnection
// Add yamux or similar for multiplexing
```

This would give us exactly the architecture we want without the proxy server middleman.

---

## 8. Recommendation

**Do not use Konnectivity.** The library is architecturally mismatched:

1. The clean module (`konnectivity-client`) is the wrong side -- it is the caller, not the agent.
2. The agent module pulls in the full Kubernetes dependency tree.
3. The mandatory proxy server adds an unnecessary extra component.
4. No client-side multiplexing -- each HTTP request needs a new gRPC tunnel.
5. The README explicitly states: "components are intended to run as standalone binaries and should not be imported as a library."

For a reverse tunnel from customer cluster agent to our backend, consider:
- **chisel** if you want an off-the-shelf solution with WebSocket transport
- **gRPC bidirectional streaming + yamux** if you want maximum control with minimal dependencies
- **A custom solution using the same pattern** (gRPC bidirectional stream for control plane, multiplexed TCP connections) would be ~200-300 lines total and perfectly fit our architecture

---

## Appendix: Key Source Files Referenced

| File | Content |
|------|---------|
| `konnectivity-client/go.mod` | Clean deps: grpc, protobuf, prometheus, klog |
| `konnectivity-client/pkg/client/client.go` | `Tunnel` interface, `CreateSingleUseGrpcTunnelWithContext()`, single-use limitation |
| `konnectivity-client/pkg/client/conn.go` | `net.Conn` implementation over gRPC packets |
| `konnectivity-client/proto/client/client.proto` | Wire protocol: 7 packet types, simple and clean |
| `pkg/agent/client.go` | Agent's `Client` struct, `Connect()`, `Serve()`, bidirectional relay |
| `pkg/agent/clientset.go` | `ClientSet` with reconnection, exponential backoff |
| `cmd/agent/app/options/options.go` | Agent config: Kubernetes leases, service account tokens, etc. |
| `go.mod` (main module) | Heavy deps: client-go, apimachinery, controller-runtime |

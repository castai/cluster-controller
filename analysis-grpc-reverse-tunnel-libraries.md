# gRPC Reverse Tunnel Libraries - Research Analysis

**Date:** 2026-02-25
**Use case:** Sidecar in k8s cluster opens outbound gRPC bidi stream to backend, receives HTTP requests (targeting kube-apiserver), proxies them locally, sends responses back.

**Current implementation:** Custom protobuf (`proxy.proto`) with `ClusterTunnel.Connect` bidi stream, custom `TunnelMessage` envelope with `HttpRequest`/`HttpResponse`/`Heartbeat` payloads, and manual `proxyRequest` logic in `internal/tunnel/client.go`.

---

## 1. jhump/grpctunnel

- **URL:** https://github.com/jhump/grpctunnel
- **Stars:** ~118
- **Latest version:** v0.3.0 (Oct 2023)
- **Maintenance:** Low activity, 32 commits total. Not actively maintained.
- **License:** Apache-2.0

**How it works:**
Tunnels gRPC-over-gRPC using a bidi streaming service (`grpctunnel.v1.TunnelService`). The client opens a tunnel to the server, and the server can send gRPC RPCs back through the tunnel (reverse tunnel). Supports affinity-based routing and connection pooling across multiple reverse tunnels.

**Fit for our use case: NO**
This library tunnels **gRPC RPCs only**, not arbitrary HTTP requests. Our use case requires sending HTTP requests (GET /api/v1/pods, etc.) to the kube-apiserver, not gRPC calls. We would need to wrap HTTP-over-gRPC ourselves on top of this, which defeats the purpose. The library also imposes its own protobuf service definition, so we'd lose control of the wire protocol.

**What it handles:** gRPC service registration, stream multiplexing, reverse tunnel lifecycle, connection pooling.
**What we'd still need:** HTTP request/response serialization, heartbeat, kube-apiserver proxying, auth metadata.

---

## 2. rancher/remotedialer

- **URL:** https://github.com/rancher/remotedialer
- **Stars:** ~317
- **Latest version:** v0.5.1 (Oct 2025)
- **Maintenance:** Actively maintained (used in production by Rancher). Bug fixes in Nov 2025.
- **License:** Apache-2.0

**How it works:**
WebSocket-based reverse tunnel. The client (in-cluster) dials the server over WebSocket, creating a `Session`. The server can then `net.Dial` through the session to reach services accessible from the client. It implements Go's `net.Dialer` interface, so the server gets a `net.Conn` that transparently tunnels through the WebSocket to the client's network. Supports HA with peer-to-peer routing across multiple server replicas.

**Fit for our use case: MODERATE (with caveats)**
This is the closest production-proven solution for the "backend accesses kube-apiserver through an agent" pattern — it's literally what Rancher uses. However:
- It uses **WebSocket**, not gRPC. If gRPC is a hard requirement (e.g., because the backend server already exposes a gRPC service for tunnel connections), this won't work directly.
- It operates at **L4 (TCP)**, not L7 (HTTP). The server gets a raw `net.Conn` and must construct HTTP requests itself. This is actually flexible — the server can use any HTTP client to talk through the tunnel.
- The tunnel protocol is proprietary (WebSocket frames with custom message types), not protobuf-based.

**What it handles:** Tunnel lifecycle, reconnection, back-pressure, HA peer routing, `net.Dialer` abstraction.
**What we'd still need:** Switch from gRPC to WebSocket on the backend side, implement auth/metadata differently, no built-in heartbeat control (handled internally).

---

## 3. kubernetes-sigs/apiserver-network-proxy (Konnectivity)

- **URL:** https://github.com/kubernetes-sigs/apiserver-network-proxy
- **Stars:** ~432
- **Latest version:** Various per-component tags
- **Maintenance:** Actively maintained by Kubernetes SIG. Production-used in every Kubernetes cluster with konnectivity enabled.
- **License:** Apache-2.0

**How it works:**
The official Kubernetes solution for control-plane-to-node connectivity. A konnectivity-agent runs on each node, opens a gRPC (mTLS) tunnel to the konnectivity-server in the control plane. The kube-apiserver uses the `konnectivity-client` library to route TCP connections through the tunnel to reach node services (kubelet, etc.). Supports gRPC and HTTP-CONNECT transport modes.

**Fit for our use case: LOW (wrong direction)**
Konnectivity solves the *opposite* direction — it lets the control plane reach the data plane. In our case, we need the backend (outside the cluster) to send HTTP requests to the kube-apiserver (inside the cluster). Additionally:
- The `konnectivity-client` library is designed for the kube-apiserver to consume — it creates single-use tunnels for TCP connections.
- The project explicitly states: "apiserver-network-proxy components are intended to run as standalone binaries and should not be imported as a library" (except `konnectivity-client`).
- The protocol is designed around TCP-level proxying, not HTTP request/response envelopes.

**What it handles:** mTLS tunnel, TCP stream multiplexing, agent lifecycle.
**What we'd still need:** Everything at the HTTP layer, reverse the direction model, custom backend integration.

---

## 4. grafana/dskit httpgrpc

- **URL:** https://github.com/grafana/dskit (package: `httpgrpc`)
- **Stars:** ~556 (dskit overall)
- **Latest version:** Actively developed (pseudo-versions, Feb 2026 latest)
- **Maintenance:** Heavily maintained by Grafana Labs. Used in production by Grafana Mimir, Loki, Cortex.
- **License:** Apache-2.0
- **Predecessor:** `weaveworks/common/httpgrpc`

**How it works:**
Defines a protobuf service for wrapping HTTP requests/responses inside gRPC:
```protobuf
service HTTP {
  rpc Handle(HTTPRequest) returns (HTTPResponse);
}
message HTTPRequest { string method; string url; repeated Header headers; bytes body; }
message HTTPResponse { int32 code; repeated Header headers; bytes body; }
message Header { string key; repeated string values; }
```
Provides helper functions: `FromHTTPRequest()`, `ToHTTPRequest()`, `WriteResponse()`, etc. for converting between `net/http` and protobuf types.

**Fit for our use case: PARTIAL (protobuf definitions only)**
The protobuf message definitions are nearly identical to what we already have in `proxy.proto`. The helper functions (`FromHTTPRequest`, `ToHTTPRequest`, `WriteResponse`) are useful but trivial to implement. However:
- It defines a **unary** RPC (`Handle`), not a bidi stream. Our tunnel needs a persistent bidi stream with multiplexed requests.
- It has no concept of tunnels, reverse connections, heartbeats, or request IDs.
- It's designed for service-to-service HTTP forwarding within a cluster, not reverse tunnel scenarios.

**What it handles:** HTTP<->protobuf conversion helpers, error mapping between HTTP status codes and gRPC codes.
**What we'd still need:** Tunnel lifecycle, bidi streaming, request multiplexing, heartbeat, reconnection, auth metadata. Essentially everything except the HTTP serialization helpers.

---

## 5. jpillora/chisel

- **URL:** https://github.com/jpillora/chisel
- **Stars:** ~15,600
- **Latest version:** v1.8.1
- **Maintenance:** Actively maintained. Very popular.
- **License:** MIT

**How it works:**
TCP/UDP tunnel transported over HTTP, secured via SSH. Single binary for both client and server. The client connects to the server over HTTP/WebSocket, establishes an SSH tunnel, and then TCP connections flow through the tunnel. Supports reverse mode where the server listens and forwards traffic through the client.

**Fit for our use case: LOW**
Chisel is a general-purpose network tunneling tool, not a library:
- It's designed as a CLI binary, not a Go library to embed.
- It operates at L4 (TCP), tunneling raw connections.
- The SSH+WebSocket transport is heavy for our use case where we just need HTTP request/response envelopes.
- No gRPC integration.
- Would require running it as a sidecar process rather than embedding it.

**What it handles:** Full tunnel lifecycle, encryption, authentication, reverse mode, SOCKS proxy.
**What we'd still need:** Entire HTTP request/response layer, integration with Go code, auth metadata.

---

## 6. flipt-io/reverst

- **URL:** https://github.com/flipt-io/reverst
- **Stars:** ~994
- **Latest version:** Multiple tags (latest ~Oct 2024)
- **Maintenance:** Moderate. Used in production by Flipt for their hybrid cloud product.
- **License:** MIT

**How it works:**
Reverse tunnel over QUIC/HTTP/3. Client dials out to register with a tunnel server. The server exposes a load-balanced HTTP endpoint that routes requests to registered clients through QUIC streams. Built on `net/http` standard-library abstractions.

**Fit for our use case: LOW-MODERATE**
- Uses QUIC/HTTP/3, not gRPC. Different protocol stack.
- Designed for exposing HTTP services publicly, which is a different pattern than our "backend sends specific HTTP requests to kube-apiserver."
- Provides a Go client library (`go.flipt.io/reverst/client`), which is good for embedding.
- But the architecture assumes the tunnel server is a dumb HTTP forwarder, not an application that constructs and sends specific API requests.

**What it handles:** Tunnel lifecycle, QUIC connections, load balancing, TLS.
**What we'd still need:** gRPC integration, HTTP request construction on backend side, kube-apiserver auth.

---

## 7. openconfig/grpctunnel

- **URL:** https://github.com/openconfig/grpctunnel
- **Stars:** ~96
- **Latest version:** v0.1.0 (Aug 2023), commit activity Jan 2025
- **Maintenance:** Low-moderate. Used in OpenConfig/gNMI ecosystem.
- **License:** Apache-2.0

**How it works:**
TCP-over-gRPC tunnel. A gRPC client and server establish a tunnel, then TCP connections are multiplexed over gRPC streams. Designed for network device management (gNMI) where devices behind firewalls dial out to a collector.

**Fit for our use case: LOW**
- Operates at L4 (TCP), not L7 (HTTP). Would need to establish TCP connections for each HTTP request.
- Designed for the OpenConfig/gNMI ecosystem, not general-purpose.
- The tunnel protocol is specific to their use case (target registration, session management for network devices).

**What it handles:** TCP stream multiplexing over gRPC, tunnel lifecycle.
**What we'd still need:** HTTP layer, request/response envelopes, heartbeat, auth.

---

## 8. open-cluster-management-io/cluster-proxy

- **URL:** https://github.com/open-cluster-management-io/cluster-proxy
- **Stars:** ~70
- **Maintenance:** Active (OCM project).
- **License:** Apache-2.0

**How it works:**
An OCM addon that wraps `kubernetes-sigs/apiserver-network-proxy` (Konnectivity) to provide L4 connectivity from a hub cluster to managed clusters. Automates konnectivity-server/agent deployment.

**Fit for our use case: LOW**
This is an operator/addon, not a library. It deploys and manages konnectivity components. Same limitations as konnectivity itself — wrong direction, L4 only, designed for multi-cluster Kubernetes management.

---

## 9. kubesphere/tower

- **URL:** https://github.com/kubesphere/tower
- **Stars:** ~99
- **Maintenance:** Low activity.
- **License:** Apache-2.0

**How it works:**
Reverse tunnel for multi-cluster KubeSphere communication. Agents in member clusters connect to a proxy in the host cluster over HTTP+SSH. The tunnel forwards traffic to the member cluster's kube-apiserver.

**Fit for our use case: LOW-MODERATE**
The pattern is very close to ours (agent in cluster, backend accesses kube-apiserver through tunnel), but:
- Uses HTTP+SSH transport, not gRPC.
- Tightly coupled to KubeSphere architecture.
- Low maintenance, not designed as a reusable library.

---

## 10. gardener/quic-reverse-http-tunnel

- **URL:** https://github.com/gardener/quic-reverse-http-tunnel
- **Stars:** ~6
- **Maintenance:** Low (last commit Feb 2024).
- **License:** Apache-2.0

**How it works:**
Reverse HTTP tunnel using QUIC. A proxy-agent opens a QUIC session to the server and listens for new streams. The server creates QUIC streams for incoming requests, which the agent forwards to the target (kube-apiserver).

**Fit for our use case: LOW**
Very niche, low stars, designed for Gardener's specific architecture. Uses QUIC, not gRPC. Not usable as a library.

---

## Summary Comparison

| Library | Stars | gRPC | HTTP Proxy | Reverse Tunnel | Embeddable Library | Actively Maintained | Fit |
|---------|-------|------|------------|----------------|-------------------|-------------------|-----|
| jhump/grpctunnel | 118 | gRPC-only | No | Yes | Yes | Low | NO |
| rancher/remotedialer | 317 | No (WebSocket) | L4 only | Yes | Yes | Yes | MODERATE |
| konnectivity | 432 | Yes | L4 only | Yes (wrong dir) | Partial | Yes | LOW |
| grafana/dskit httpgrpc | 556 | Yes | Unary only | No | Yes | Yes | PARTIAL |
| chisel | 15.6k | No | L4 only | Yes | No (binary) | Yes | LOW |
| flipt-io/reverst | 994 | No (QUIC) | Yes (HTTP/3) | Yes | Yes | Moderate | LOW-MOD |
| openconfig/grpctunnel | 96 | Yes | L4 only | Yes | Yes | Low | LOW |
| cluster-proxy (OCM) | 70 | Yes | L4 only | Yes | No (addon) | Yes | LOW |
| kubesphere/tower | 99 | No (SSH) | L4 only | Yes | No | Low | LOW |
| gardener/quic-tunnel | 6 | No (QUIC) | Yes | Yes | No | Low | LOW |

---

## Recommendation

**None of the libraries are a drop-in replacement for the current custom implementation.** Here's why:

### The core problem
Our use case sits at the intersection of three requirements:
1. **gRPC bidi stream** as the tunnel transport (outbound from cluster)
2. **HTTP request/response envelopes** multiplexed over that stream (not raw TCP)
3. **Application-layer control** (heartbeats, auth metadata, request IDs)

No existing library combines all three. The landscape breaks down as:
- **gRPC tunnel libraries** (jhump, openconfig) tunnel gRPC or TCP, not HTTP requests
- **HTTP tunnel libraries** (remotedialer, chisel, reverst) use WebSocket/QUIC/SSH, not gRPC
- **HTTP-over-gRPC libraries** (dskit/httpgrpc) provide message definitions but no tunnel

### What to consider

**Option A: Keep the current custom implementation (recommended)**
The existing code in `internal/tunnel/client.go` is ~280 lines total. The protobuf definition is clean and minimal. The custom implementation gives full control over:
- Wire protocol (can evolve independently)
- Auth metadata (cluster-id, api-key in gRPC metadata)
- Heartbeat semantics
- Request size limits
- Error handling

The amount of code saved by adopting a library would be minimal, and the coupling/complexity cost would be significant.

**Option B: Adopt `grafana/dskit/httpgrpc` for message definitions only**
If standardization of the HTTP-over-gRPC message format is desired, `dskit/httpgrpc` provides battle-tested protobuf definitions and `FromHTTPRequest`/`ToHTTPRequest` helpers. But the current `proxy.proto` definitions are already nearly identical, so the benefit is marginal.

**Option C: Adopt `rancher/remotedialer` (if willing to switch from gRPC to WebSocket)**
If the backend can accept WebSocket connections instead of gRPC streams, remotedialer is the most production-proven solution for this exact pattern. It provides a `net.Dialer` on the server side that transparently tunnels to the client's network. The trade-off is losing gRPC as the transport and adopting a WebSocket-based protocol.

### Bottom line
The current ~280-line custom implementation is the right approach. The tunnel protocol is simple enough that a library adds more coupling than value. The only thing worth borrowing are patterns (heartbeat, reconnection with backoff) which are already implemented.

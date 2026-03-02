# Open-Source gRPC-Based Reverse Tunnel/Proxy Solutions

Research date: 2026-03-02

---

## 1. jhump/grpctunnel

- **GitHub:** https://github.com/jhump/grpctunnel
- **Stars:** ~118
- **Language:** Go
- **License:** Apache 2.0

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | No (gRPC-over-gRPC only, not HTTP-over-gRPC) |
| Streaming responses            | Yes (full-duplex bidirectional stream) |
| Multiplexing                   | Yes (multiple RPC streams over single tunnel) |
| Forward tunneling              | Yes |
| Reverse tunneling              | Yes |

### Summary

This library tunnels **gRPC calls over a gRPC bidirectional stream**. It does NOT proxy raw HTTP requests -- it specifically tunnels gRPC service calls. The tunnel is a `grpctunnel.v1.TunnelService` with bidirectional streaming RPCs for both forward and reverse directions.

**Reverse tunnel use case:** A server behind NAT dials a central router and registers itself. The router then forwards gRPC requests to the server through the reverse tunnel. The gRPC client that created the tunnel acts as the server.

### Maintenance

Moderately maintained. 32 commits on main. Not highly active but the codebase is stable/mature.

### Relevance for custom HTTP-over-gRPC tunnel

**Low-Medium.** If you only need to tunnel gRPC calls, this is an excellent fit. If you need to tunnel arbitrary HTTP requests over a gRPC stream, you would need to build an HTTP-to-gRPC adapter on top of it, which largely defeats the purpose.

---

## 2. openconfig/grpctunnel

- **GitHub:** https://github.com/openconfig/grpctunnel
- **Stars:** ~96
- **Language:** Go
- **License:** Apache 2.0

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | No (TCP-over-gRPC) |
| Streaming responses            | Yes (bidirectional TCP stream) |
| Multiplexing                   | Yes (multiple TCP sessions over single tunnel) |
| Forward tunneling              | Yes |
| Reverse tunneling              | Yes |

### Summary

A **TCP-over-gRPC** tunnel. Lower level than jhump/grpctunnel -- it tunnels raw TCP connections over gRPC streams. Designed for network equipment management (OpenConfig/gNMI ecosystem). You could in principle run HTTP over the TCP tunnel, but you'd need to handle HTTP framing yourself.

### Maintenance

Backed by the OpenConfig organization (Google-affiliated). 118 commits. Last release v0.1.0 (Aug 2023). Moderate activity.

### Relevance

**Low.** Too low-level for HTTP proxying. Designed for network infrastructure, not application-level HTTP tunneling.

---

## 3. Chisel (jpillora/chisel)

- **GitHub:** https://github.com/jpillora/chisel
- **Stars:** ~15,700
- **Language:** Go
- **License:** MIT

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | **No** -- uses HTTP/WebSocket + SSH |
| Streaming responses            | Yes (TCP/UDP streams) |
| Multiplexing                   | Yes (multiple TCP/UDP tunnels over single connection) |
| Forward tunneling              | Yes |
| Reverse tunneling              | Yes |
| SOCKS5 proxy                   | Yes |

### Summary

Fast TCP/UDP tunnel transported over HTTP, secured via SSH. Single binary for both client and server. Very popular and battle-tested. **Does NOT use gRPC transport** -- uses WebSocket over HTTP with SSH encryption. Supports reverse port forwarding and SOCKS5 proxying.

### Maintenance

Very actively maintained. 246 commits, 15.7k stars, 1.6k forks. Large community.

### Relevance

**Not directly applicable** if gRPC transport is a requirement. However, worth noting as the gold standard for simple reverse tunnels in Go. If gRPC transport is not strictly required (e.g., you just need a reverse tunnel and already have an HTTP/WS path), Chisel is the most mature option.

---

## 4. Inlets (alexellis/inlets)

- **GitHub:** https://github.com/inlets (organization)
- **Stars:** OSS version had ~14k+ stars (now archived); inlets-pro is commercial
- **Language:** Go
- **License:** MIT (OSS), Commercial (Pro)

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | **No** -- uses WebSocket transport |
| Streaming responses            | Yes |
| Multiplexing                   | Yes |
| Forward tunneling              | Yes |
| Reverse tunneling              | Yes |
| L4 TCP tunneling (Pro)         | Yes |
| Kubernetes operator            | Yes (inlets-operator) |

### Summary

Reverse proxy and service tunnel. The OSS version uses WebSocket transport to tunnel HTTP. **Does NOT use gRPC.** inlets Pro adds L4/TCP proxying and automatic TLS. Has a Kubernetes operator (`inlets-operator`) that provisions cloud VMs and creates tunnels automatically.

### Maintenance

Actively maintained as a commercial product (inlets.dev). The original OSS version (alexellis/inlets) has been archived in favor of the commercial inlets-pro.

### Relevance

**Not applicable** if gRPC transport is a hard requirement. Uses WebSocket, not gRPC. The Kubernetes operator pattern is interesting but the core transport doesn't match.

---

## 5. Kubernetes apiserver-network-proxy (Konnectivity)

- **GitHub:** https://github.com/kubernetes-sigs/apiserver-network-proxy
- **Stars:** ~431
- **Language:** Go
- **License:** Apache 2.0

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | **Yes** (primary use case) |
| Streaming responses            | Yes |
| Multiplexing                   | Yes (multiple HTTP connections over single gRPC stream) |
| Forward tunneling              | N/A (agent-initiated) |
| Reverse tunneling              | **Yes** (agent dials server, server proxies requests back) |
| Kubernetes-native              | **Yes** (core K8s component) |

### Summary

**This is the closest match to a custom HTTP-over-gRPC reverse tunnel.** Konnectivity is the official Kubernetes solution for proxying API server requests to cluster nodes through a gRPC tunnel. The architecture is:

1. **Konnectivity Agent** (runs on worker nodes) dials out to the Konnectivity Server via gRPC
2. **Konnectivity Server** (runs in control plane) receives HTTP requests from the API server
3. Server forwards HTTP requests through the gRPC tunnel to the agent
4. Agent proxies the request to the target service/pod

Supports multiple modes: `HTTP over GRPC`, `HTTP over GRPC+UDS`, and `HTTP-CONNECT`.

### Maintenance

**Very actively maintained.** Part of kubernetes-sigs. Aligns releases with Kubernetes versions. Used in production by every managed Kubernetes provider. OpenShift maintains its own fork.

### Relevance

**HIGH.** This is the most battle-tested, production-grade HTTP-over-gRPC reverse tunnel in the Go ecosystem. It is purpose-built for the exact pattern of "agent behind NAT dials central server, central server proxies HTTP requests through gRPC tunnel to agent." The `konnectivity-client` package can be used as a library.

---

## 6. HARP (SimonWaldherr/HARP)

- **GitHub:** https://github.com/SimonWaldherr/HARP
- **Stars:** ~9
- **Language:** Go
- **License:** GPL-3.0

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | **Yes** |
| Streaming responses            | Yes (bidirectional gRPC stream) |
| Multiplexing                   | Yes (multiple backends, route-based) |
| Reverse tunneling              | **Yes** (backends behind NAT connect out to proxy) |
| HTTP/3 support                 | Yes |
| Caching                        | Yes (in-memory + disk) |

### Summary

HARP (HTTP AutoRegister Reverse Proxy) is a dynamic reverse proxy where backends connect via gRPC, register their HTTP endpoints with per-route authentication, and receive forwarded HTTP requests over the gRPC stream.

Architecture: Public HTTP server receives requests -> matches to registered route -> forwards request via gRPC to backend -> backend returns response via gRPC -> proxy relays to client.

### Maintenance

Small project. 35 commits, 9 stars. Latest release v1.2.1 (Aug 2025). Single maintainer.

### Relevance

**Medium.** Conceptually very close to what a custom solution would look like. The architecture pattern (HTTP-in, gRPC-tunnel-to-backend, HTTP-response-back) is exactly right. However: very small community, GPL-3.0 license (viral), and single maintainer. Good as reference architecture, risky as a dependency.

---

## 7. kwakubiney/outbound

- **GitHub:** https://github.com/kwakubiney/outbound
- **Stars:** ~4
- **Language:** Go

### Capabilities

| Feature                        | Supported |
|-------------------------------|-----------|
| HTTP proxying over gRPC stream | **Yes** |
| Streaming responses            | Yes (long-lived gRPC stream) |
| Multiplexing                   | Yes (multiple services per agent, header-based routing) |
| Reverse tunneling              | **Yes** |

### Summary

A reverse HTTP tunnel where an "edge" server accepts HTTP requests and dispatches them over gRPC to agents. Agents register named services and proxy inbound requests to local ports. Routing is header-based (X-Outbound-Agent, X-Outbound-Service).

### Maintenance

Very new (created Feb 2026). 22 commits, 4 stars. Essentially a proof-of-concept.

### Relevance

**Low as dependency, Medium as reference.** Too immature for production use, but the design pattern and code are a clean, minimal example of exactly the "HTTP reverse proxy over gRPC stream" pattern.

---

## 8. Other Notable Projects

### mwitkow/grpc-proxy
- **GitHub:** https://github.com/mwitkow/grpc-proxy
- **Stars:** ~1,000
- gRPC reverse proxy for routing gRPC calls. Does NOT do HTTP-over-gRPC or reverse tunneling. Forward-proxy only.

### siderolabs/grpc-proxy (fork of mwitkow)
- **GitHub:** https://github.com/siderolabs/grpc-proxy
- **Stars:** ~82
- Adds one-to-many proxying with result aggregation. Same limitations -- forward gRPC proxy only.

### diamondo25/grpc-tcp-tunnel
- **GitHub:** https://github.com/diamondo25/grpc-tcp-tunnel
- **Stars:** ~11
- TCP tunnel over gRPC streaming. Last release 2019. Abandoned.

### Weave "HTTP over gRPC" pattern
- **Blog:** https://www.weave.works/blog/turtles-way-http-grpc/
- Not a library, but a well-documented pattern for wrapping HTTP Request/Response as a gRPC service. Used in Weave Cloud's AuthFE reverse proxy. Implements `http.Handler` on the client side and proxies to `http.Handler` on the server side over gRPC.

---

## Comparison Matrix

| Project | Stars | HTTP-over-gRPC | Reverse Tunnel | Streaming | Multiplexing | Maintained | K8s Native | License |
|---|---|---|---|---|---|---|---|---|
| **Konnectivity** (apiserver-network-proxy) | 431 | **Yes** | **Yes** | Yes | Yes | **Very Active** | **Yes** | Apache 2.0 |
| jhump/grpctunnel | 118 | No (gRPC-over-gRPC) | Yes | Yes | Yes | Moderate | No | Apache 2.0 |
| openconfig/grpctunnel | 96 | No (TCP-over-gRPC) | Yes | Yes | Yes | Moderate | No | Apache 2.0 |
| Chisel | 15,700 | No (WebSocket) | Yes | Yes | Yes | **Very Active** | No | MIT |
| Inlets | 14k+ (archived) | No (WebSocket) | Yes | Yes | Yes | Commercial | Yes (operator) | MIT/Commercial |
| HARP | 9 | **Yes** | **Yes** | Yes | Yes | Low | No | GPL-3.0 |
| outbound | 4 | **Yes** | **Yes** | Yes | Yes | New | No | Unknown |
| mwitkow/grpc-proxy | 1,000 | No | No | Yes | No | Low | No | Apache 2.0 |

---

## Recommendations

### If you need HTTP-over-gRPC reverse tunnel:

1. **Best production option: Konnectivity (apiserver-network-proxy)**
   - Battle-tested in every Kubernetes cluster worldwide
   - Exact same pattern: agent behind NAT -> dials gRPC to server -> server proxies HTTP through tunnel
   - The `konnectivity-client` Go package can be imported as a library
   - Apache 2.0 licensed
   - Caveat: tightly coupled to Kubernetes API server concepts; may need adaptation for non-K8s use

2. **Best reference architecture: HARP + outbound + Weave blog post**
   - Study these for the pattern of wrapping HTTP req/resp into gRPC stream messages
   - HARP's proto definitions and flow are clean examples
   - The Weave blog post explains the `http.Handler`-based approach elegantly

3. **Custom implementation may still be warranted if:**
   - You need tight integration with your specific agent/controller protocol
   - You want to avoid the Konnectivity coupling to K8s API machinery
   - You need custom multiplexing, auth, or routing logic
   - The tunnel is a small part of a larger bidirectional gRPC stream you already maintain

### If gRPC transport is NOT a hard requirement:

- **Chisel** is the clear winner -- 15.7k stars, battle-tested, simple, MIT licensed
- Much less complexity than building a gRPC-based tunnel

### Cost/benefit of custom vs. library:

| Factor | Custom | Konnectivity (library) | Chisel (non-gRPC) |
|---|---|---|---|
| Dev effort | High (weeks) | Medium (days for adaptation) | Low (hours) |
| Maintenance burden | High (you own it) | Low (upstream maintained) | Low |
| Flexibility | Total | Limited by K8s patterns | Limited by WebSocket transport |
| Production readiness | Must prove | Proven at massive scale | Proven at scale |
| gRPC integration | Native | Native | Separate transport |

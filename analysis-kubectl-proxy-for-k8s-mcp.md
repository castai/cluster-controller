# kubectlproxy + kubernetes-mcp-server: Feasibility Analysis

## Critical Finding: The MCP server does NOT use kubectl

The [kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server) uses **client-go directly** - it speaks the native Kubernetes REST API, not kubectl CLI commands.

Its `KubernetesClient` interface embeds:
- `kubernetes.Interface` (standard client-go clientset)
- `dynamic.Interface` (for generic resources)
- `discovery.CachedDiscoveryInterface` (API discovery)
- `metricsv1beta1.MetricsV1beta1Client` (metrics API)

Concrete API calls made by the MCP tools:

| MCP Tool | Actual K8s API Call |
|---|---|
| `pods_list` | `CoreV1().Pods(ns).List()` → `GET /api/v1/namespaces/{ns}/pods` |
| `pods_get` | `DynamicClient().Resource(podGVR).Get()` → `GET /api/v1/namespaces/{ns}/pods/{name}` |
| `pods_log` | `CoreV1().Pods(ns).GetLogs(name, opts)` → `GET /api/v1/namespaces/{ns}/pods/{name}/log` |
| `pods_exec` | `CoreV1().RESTClient().Post()` + SPDY/WebSocket → `POST /api/v1/namespaces/{ns}/pods/{name}/exec` |
| `pods_delete` | `CoreV1().Pods(ns).Delete()` → `DELETE /api/v1/namespaces/{ns}/pods/{name}` |
| `pods_top` | `MetricsV1beta1().PodMetricses(ns).Get()` → `GET /apis/metrics.k8s.io/v1beta1/...` |
| `pods_run` | `CoreV1().Pods(ns).Create()` → `POST /api/v1/namespaces/{ns}/pods` |
| `events_list` | `CoreV1().Events(ns).List()` → `GET /api/v1/events` |
| `namespaces_list` | `CoreV1().Namespaces().List()` → `GET /api/v1/namespaces` |
| `nodes_top` | `MetricsV1beta1().NodeMetricses().List()` → `GET /apis/metrics.k8s.io/v1beta1/nodes` |
| `nodes_log` | Proxy via node API → `GET /api/v1/nodes/{name}/proxy/logs/...` |
| `resources_list` | `DynamicClient().Resource(gvr).List()` → `GET /apis/{group}/{version}/{resource}` |
| `helm_*` | Helm SDK (also uses client-go REST config internally) |

### What this means

**Your kubectlproxy cannot just accept kubectl CLI args.** It must expose the **standard Kubernetes REST API** so that client-go can talk to it as if it were a real kube-apiserver.

The kubernetes-mcp-server connects via kubeconfig:
```yaml
clusters:
- cluster:
    server: https://<your-kubectlproxy>/clusters/{clusterId}
  name: customer-cluster
```

Then it makes HTTP calls like `GET /api/v1/namespaces/default/pods` to that URL.

---

## Architecture: kubectlproxy as a K8s API Reverse Proxy

```
┌──────────────────────────┐
│ kubernetes-mcp-server    │
│                          │
│ client-go clientset      │
│ kubeconfig.server =      │
│   kubectlproxy URL       │
└────────────┬─────────────┘
             │  Standard K8s REST API
             │  GET /api/v1/namespaces/default/pods
             ▼
┌──────────────────────────┐
│ kubectlproxy (backend)   │
│                          │
│ - Receives raw K8s API   │
│   HTTP requests          │
│ - Routes by cluster ID   │
│ - Auth & RBAC            │
│ - Forwards through       │
│   tunnel to cluster-ctrl │
└────────────┬─────────────┘
             │  Tunnel (WebSocket / gRPC stream / SSH)
             │
             ▼
┌──────────────────────────┐
│ cluster-controller       │
│ (customer cluster)       │
│                          │
│ - Receives proxied req   │
│ - Forwards to real       │
│   kube-apiserver         │
│   (https://kubernetes.   │
│    default.svc)          │
│ - Returns response       │
└──────────────────────────┘
```

### Why the existing polling model won't work here

The current action system (poll every 5s → execute → ack) is designed for fire-and-forget operations. The MCP server needs:
- **Synchronous request-response** (client-go waits for HTTP response)
- **Low latency** (not 5 seconds)
- **HTTP semantics preserved** (status codes, headers, streaming for logs/exec)
- **Connection upgrades** (SPDY/WebSocket for `pods_exec`)

You need a **persistent bidirectional channel** between kubectlproxy and cluster-controller.

---

## Approaches

### Approach A: Reverse tunnel from cluster-controller to backend

Cluster-controller opens a persistent outbound connection to the backend. kubectlproxy routes K8s API requests through it.

```
cluster-controller ──outbound WebSocket/gRPC──▶ kubectlproxy (backend)

kubectlproxy receives K8s API request
  → finds the tunnel for the target cluster
  → forwards the HTTP request through the tunnel
  → cluster-controller receives it
  → cluster-controller forwards to https://kubernetes.default.svc
  → response flows back through the tunnel
```

**This is what Rancher, Teleport, Loft/vCluster, and Cloudflare Tunnel do.**

Implementation options for the tunnel:

| Option | How it works | Complexity | Streaming support |
|---|---|---|---|
| **WebSocket tunnel** | CC opens WS to backend, HTTP requests multiplexed over it | Medium | Yes (multiplexed) |
| **gRPC bidirectional stream** | CC opens gRPC stream, requests/responses as messages | Medium | Yes |
| **HTTP/2 reverse proxy** | CC opens HTTP/2 conn, backend pushes requests via stream | Medium-High | Yes |
| **chisel / frp / rathole** | Use existing tunnel library/tool | Low (integration) | Yes |
| **Kubernetes API proxy libraries** | e.g., rancher/remotedialer | Low-Medium | Yes |

**Recommended: Look at [rancher/remotedialer](https://github.com/rancher/remotedialer)**. This is exactly the library Rancher built for this purpose - it allows a downstream cluster agent to dial back to the management server, creating a reverse tunnel that can proxy HTTP requests. It's battle-tested and designed for this exact use case.

#### Changes needed in cluster-controller:

1. **New tunnel client component** - opens persistent connection to backend on startup
   - Endpoint: `wss://api.cast.ai/v1/clusters/{clusterId}/tunnel` (or similar)
   - Auth: same X-API-Key
   - Reconnection with backoff (reuse existing `waitext.Retry`)
2. **HTTP proxy handler** - receives requests from the tunnel, forwards to `https://kubernetes.default.svc`
   - Uses the in-cluster service account token (already available via client-go)
   - Preserves HTTP method, path, headers, body
   - Streams response back

#### Changes needed in backend (kubectlproxy service):

1. **Tunnel server** - accepts WebSocket/gRPC connections from cluster-controllers
   - Maps cluster ID → active tunnel connection
2. **K8s API proxy endpoint** - `https://kubectlproxy.cast.ai/clusters/{clusterId}/api/...`
   - Strips the `/clusters/{clusterId}` prefix
   - Forwards the raw K8s API request through the tunnel
   - Returns the response to the caller (kubernetes-mcp-server)
3. **Auth** - validates the caller's credentials, enforces RBAC

### Approach B: Run kubernetes-mcp-server inside the customer cluster

Deploy kubernetes-mcp-server as a sidecar or adjacent pod in the customer cluster. It connects directly to the kube-apiserver using in-cluster credentials.

```
┌─ Customer Cluster ──────────────────────────┐
│                                              │
│  kubernetes-mcp-server ──▶ kube-apiserver    │
│       │                                      │
│       │ MCP protocol (SSE/stdio)             │
│       ▼                                      │
│  cluster-controller ──outbound──▶ backend    │
│                                              │
└──────────────────────────────────────────────┘
         │
         ▼
    Backend relays MCP protocol to chatbot
```

**Pros:** No K8s API proxy needed, MCP server talks directly to real kube-apiserver.
**Cons:** Need to get MCP protocol (SSE) from inside the cluster to the backend. Need to deploy + manage the MCP server in every customer cluster. More components to maintain.

### Approach C: Give kubectlproxy direct kube-apiserver access (no cluster-controller involvement)

If you already have the customer cluster's kubeconfig/credentials stored in your backend:

```
kubernetes-mcp-server
    ▼
kubectlproxy (has stored kubeconfigs)
    ▼
customer kube-apiserver (direct, over internet)
```

**Pros:** Simplest, no tunnel, no cluster-controller changes.
**Cons:** Requires the kube-apiserver to be internet-accessible (not always true for private clusters). Requires storing customer credentials in your backend.

---

## Recommendation

### If customer clusters have internet-accessible API servers

**Approach C** is simplest. kubectlproxy stores kubeconfigs, kubernetes-mcp-server (or kubectlproxy itself) connects directly. No cluster-controller changes needed.

### If customer clusters are private / you don't store kubeconfigs

**Approach A** (reverse tunnel) is the right solution. Here's the implementation plan:

#### Phase 1: Tunnel infrastructure

**cluster-controller side:**
- New package `internal/tunnel/` with a `Client` that opens a persistent outbound connection
- HTTP reverse proxy that forwards tunnel requests to `https://kubernetes.default.svc`
- Started alongside the main controller in `cmd/controller/run.go`
- Config: `TUNNEL_ENABLED`, `TUNNEL_URL` (defaults to `{API_URL}/tunnel`)

**Backend side:**
- New `kubectlproxy` service
- Tunnel server accepting connections from cluster-controllers
- K8s API proxy endpoints: `GET/POST/PUT/DELETE /clusters/{clusterId}/api/...`
- Auth + RBAC + audit logging

#### Phase 2: kubernetes-mcp-server integration

- Generate kubeconfigs on the fly: `server: https://kubectlproxy.cast.ai/clusters/{clusterId}`
- Configure kubernetes-mcp-server with these kubeconfigs
- Enable `--read-only` or `--disable-destructive` for safety
- Expose MCP server to chatbot

#### Phase 3: Developer access

- Same tunnel + proxy serves developer kubectl access
- Custom kubeconfig generation endpoint
- kubectl plugin for convenience

---

## K8s API Surface to support

Based on the kubernetes-mcp-server tools, the minimum K8s API surface your proxy needs:

### Must have (core tools)
| API Path | Method | Used by |
|---|---|---|
| `/api/v1/namespaces` | GET | `namespaces_list` |
| `/api/v1/namespaces/{ns}/pods` | GET | `pods_list_in_namespace` |
| `/api/v1/pods` | GET | `pods_list` (all namespaces) |
| `/api/v1/namespaces/{ns}/pods/{name}` | GET | `pods_get` |
| `/api/v1/namespaces/{ns}/pods/{name}` | DELETE | `pods_delete` |
| `/api/v1/namespaces/{ns}/pods/{name}/log` | GET | `pods_log` |
| `/api/v1/namespaces/{ns}/pods` | POST | `pods_run` |
| `/api/v1/events` | GET | `events_list` |
| `/api/v1/namespaces/{ns}/events` | GET | `events_list` |
| `/api` | GET | API discovery |
| `/apis` | GET | API discovery |

### Nice to have
| API Path | Method | Used by |
|---|---|---|
| `/api/v1/namespaces/{ns}/pods/{name}/exec` | POST + upgrade | `pods_exec` |
| `/apis/metrics.k8s.io/v1beta1/pods` | GET | `pods_top` |
| `/apis/metrics.k8s.io/v1beta1/nodes` | GET | `nodes_top` |
| `/api/v1/nodes/{name}/proxy/logs/...` | GET | `nodes_log` |

### Not needed for read-only
| Helm operations | - use Helm SDK which needs broad API access |

**However**, if you use a raw HTTP reverse proxy (Approach A), you don't need to implement individual endpoints - you just forward everything. The proxy is transparent. The RBAC/filtering layer in kubectlproxy can whitelist which API paths are allowed.

---

## Security Model

```
kubernetes-mcp-server → kubectlproxy → tunnel → cluster-controller → kube-apiserver
                             │
                    ┌────────┴────────┐
                    │  Security Layer  │
                    │                  │
                    │ 1. Caller auth   │  (API key, OAuth token)
                    │ 2. Cluster authz │  (can caller access this cluster?)
                    │ 3. API whitelist │  (only GET on pods/logs/events/etc.)
                    │ 4. Namespace     │  (restrict to allowed namespaces)
                    │    restrictions  │
                    │ 5. Rate limiting │
                    │ 6. Audit log     │  (who ran what, when, on which cluster)
                    │ 7. Response size │  (max 10MB response)
                    │    limits        │
                    └─────────────────┘
```

The cluster-controller's service account in the customer cluster determines what the proxy can actually do. The existing RBAC of the service account is the final enforcement layer.

---

## Comparison with Existing Solutions

| Solution | How it works | Similarity |
|---|---|---|
| **Rancher** | Agent in downstream cluster opens WebSocket tunnel to Rancher server. Rancher proxies kubectl/API requests through tunnel. | Almost identical to Approach A |
| **Teleport** | Agent opens reverse tunnel. Teleport proxy forwards kubectl sessions through it. | Same pattern, more features (audit, RBAC, session recording) |
| **Loft / vCluster** | Virtual clusters with API proxy. | Similar API proxying, different use case |
| **Lens (OpenLens)** | Desktop app that connects directly to clusters via kubeconfig. | Direct connection, no proxy |
| **Cloudflare Tunnel** | cloudflared agent opens tunnel, routes HTTP traffic through Cloudflare. | Same tunnel pattern, generic HTTP |

Your approach with cluster-controller + kubectlproxy is essentially **the Rancher pattern** applied to your CAST.AI architecture. Consider using or referencing [rancher/remotedialer](https://github.com/rancher/remotedialer) for the tunnel implementation.

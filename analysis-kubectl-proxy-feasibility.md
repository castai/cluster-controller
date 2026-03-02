# Kubectl Proxy via Cluster Controller - Feasibility Analysis

## Current State

### What the cluster-controller already has

**Kubectl sidecar** (`internal/kubectl/server.go`, `cmd/sidecar/`):
- HTTP server on `127.0.0.1:7070` (localhost only)
- `POST /kubectl` endpoint accepting `{"args": ["get", "pods", "-n", "default"]}`
- Returns `{"stdout": "...", "stderr": "...", "exitCode": 0}`
- Whitelisted subcommands: `get`, `logs`, `describe`, `events`, `top`
- 30s command timeout (configurable)
- Runs as a **separate binary** (`cmd/sidecar/main.go`) - intended to be a sidecar container in the same pod

**Communication model** (`internal/castai/client.go`):
- Cluster-controller **polls** the backend every 5s: `GET /v1/kubernetes/clusters/{clusterId}/actions`
- Backend responds with a list of `ClusterAction` items (a tagged union)
- Controller dispatches each action to a handler, then acknowledges: `POST .../actions/{actionId}/ack`
- This is a **fire-and-forget action model** - backend sends an action, controller executes it and reports success/error
- There is **no mechanism to return arbitrary output** (stdout/stderr) back to the backend

### What's missing for your use case

| Capability | Status |
|---|---|
| Execute kubectl commands in customer cluster | **EXISTS** (sidecar) |
| Return command output to backend | **MISSING** - ack only reports error string, not stdout/stderr |
| On-demand / low-latency command execution | **MISSING** - 5s polling latency |
| Interactive/streaming output (e.g. `kubectl logs -f`) | **MISSING** |
| Backend-initiated request → cluster response | **MISSING** - only pull-based model exists |

---

## Question 1: Can cluster-controller send kubectl output to the backend?

**Yes, absolutely feasible.** Two approaches:

### Approach A: New action type + response endpoint (minimal change, fits existing architecture)

Add a new action type `ActionKubectl` to the existing polling-based action system:

```
Backend → GetActions() → [ActionKubectl{args: ["logs", "pod-x", "-n", "prod"], requestID: "abc123"}]
Controller → executes kubectl → acks action
Controller → POST /v1/kubernetes/clusters/{clusterId}/kubectl/{requestID}/response
             body: {stdout: "...", stderr: "...", exitCode: 0}
```

**Changes needed in cluster-controller:**
1. Add `ActionKubectl` to `ClusterAction` union in `internal/castai/types.go`
2. Add `KubectlHandler` in `internal/actions/` that calls the existing `kubectl.Server` logic (or directly runs `exec.Command`)
3. Add `SendKubectlResponse(ctx, requestID, response)` method to `CastAIClient` interface
4. Register handler in `internal/actions/actions.go`

**Changes needed in backend:**
1. New API endpoint: `POST /v1/kubernetes/clusters/{clusterId}/kubectl` to queue the action
2. New API endpoint: `GET /v1/kubernetes/clusters/{clusterId}/kubectl/{requestID}/response` to retrieve the result (or use callback/webhook)
3. Storage for request-response correlation (Redis/DB with TTL)

**Pros:** Fits perfectly into existing architecture, minimal cluster-controller changes, reuses battle-tested polling + action system.
**Cons:** 5-second latency (polling interval), not suitable for streaming.

### Approach B: WebSocket/gRPC bidirectional channel (lower latency, more complex)

Replace or supplement the polling model with a persistent bidirectional connection:

```
Controller ←→ WebSocket/gRPC stream ←→ Backend
Backend pushes: {type: "kubectl", args: [...], requestID: "abc"}
Controller responds: {requestID: "abc", stdout: "...", exitCode: 0}
```

**Pros:** Sub-second latency, enables streaming (`kubectl logs -f`), more natural request-response semantics.
**Cons:** Significantly more complex, requires new connection management, reconnection logic, potentially new dependencies (gRPC streaming).

### Recommendation

**Start with Approach A.** It requires minimal changes to the cluster-controller (4-5 files), fits the existing architecture, and the 5s latency is acceptable for debugging/troubleshooting commands. You can evolve to Approach B later if streaming or lower latency becomes critical.

---

## Question 2: Developers using kubectl pointed at backend

This is essentially building a **Kubernetes API proxy/gateway** in your backend. The developer's `~/.kube/config` would look like:

```yaml
apiVersion: v1
clusters:
- cluster:
    server: https://api.cast.ai/kubectl-proxy/clusters/{clusterId}
    # or: https://kubectl.cast.ai/clusters/{clusterId}
  name: customer-cluster-via-castai
contexts:
- context:
    cluster: customer-cluster-via-castai
    user: castai-dev
  name: my-customer
users:
- user:
    token: <castai-api-key-or-bearer-token>
  name: castai-dev
```

### Architecture: `kubectlproxy` service

```
Developer's kubectl
    │
    │  HTTPS (Kubernetes API protocol)
    ▼
┌─────────────────────────┐
│  kubectlproxy (backend) │
│                         │
│  - Receives K8s API     │
│    requests             │
│  - Translates to        │
│    kubectl args         │  OR: Translates to raw K8s API calls
│  - Queues as action     │      forwarded to cluster-controller
│  - Waits for response   │
│  - Returns to kubectl   │
└─────────────────────────┘
    │
    │  REST (existing actions API)
    ▼
┌─────────────────────────┐
│  cluster-controller     │
│  (customer cluster)     │
│  - Polls for actions    │
│  - Executes kubectl     │
│  - Returns output       │
└─────────────────────────┘
```

### Two sub-approaches for the kubectlproxy:

#### Sub-approach 2A: kubectl text passthrough

- kubectlproxy receives the raw kubectl request
- Translates it to kubectl CLI args (e.g., `GET /api/v1/namespaces/default/pods` → `kubectl get pods -n default -o json`)
- Sends as `ActionKubectl` to cluster-controller
- Returns stdout as HTTP response

**Problem:** kubectl speaks the Kubernetes API protocol (REST with watches, WebSockets for exec/attach, etc.) - translating raw K8s API calls to CLI args is lossy and fragile.

#### Sub-approach 2B: Kubernetes API reverse proxy

- kubectlproxy implements a subset of the Kubernetes API server interface
- Forwards the raw HTTP request body to the cluster-controller
- Cluster-controller forwards it to the real kube-apiserver using its in-cluster credentials
- Returns the response

This is essentially what tools like [Teleport](https://goteleport.com/), [Loft](https://loft.sh/), or [Rancher](https://rancher.com/) do.

**Changes needed in cluster-controller:**
- A new handler (or extension of sidecar) that can proxy raw HTTP requests to the kube-apiserver
- Something like: receive request path + method + body → forward to `https://kubernetes.default.svc` → return response

**This is a significantly larger effort** than just kubectl command execution. For v1, I'd recommend:

1. **Don't try to emulate the full K8s API** - instead, build a custom CLI tool or kubectl plugin that communicates with your kubectlproxy using your own protocol
2. Or limit it to read-only text-based commands (`get`, `describe`, `logs`, `events`, `top`) which map cleanly to CLI args

### Recommendation for developer access

**Phase 1:** Build a simple CLI tool or kubectl plugin (`kubectl castai logs pod-x --cluster <id>`) that talks to your kubectlproxy backend, which in turn uses the action system. This avoids the complexity of emulating a K8s API server.

**Phase 2 (optional):** If full `kubectl` compatibility is important, build a K8s API reverse proxy - but this is a significant project (auth, watch streams, exec WebSockets, etc.).

---

## Question 3: Exposing kubectlproxy to chatbot (A2A) and kube-mcp

### For a chatbot via A2A protocol

Your `kubectlproxy` service would expose an **A2A (Agent-to-Agent) endpoint** that your chatbot can call:

```
Chatbot (A2A client)
    │
    │  A2A protocol (JSON-RPC over HTTP)
    ▼
┌─────────────────────────────────────┐
│  kubectlproxy (A2A server)          │
│                                     │
│  Skills/capabilities:               │
│  - kubectl_get(resource, namespace) │
│  - kubectl_logs(pod, namespace, ...)│
│  - kubectl_describe(resource, name) │
│  - kubectl_events(namespace)        │
│  - kubectl_top(resource)            │
│  - kubectl_debug(pod, image)        │
└─────────────────────────────────────┘
    │
    │  Internal API / Actions
    ▼
  cluster-controller (in customer cluster)
```

The A2A server would expose an "Agent Card" describing its capabilities, and each kubectl operation would be a skill the chatbot can invoke. The chatbot sends a task, kubectlproxy executes it via the cluster-controller, and returns the result.

### For kube-mcp (Model Context Protocol)

MCP is simpler - kubectlproxy would be an **MCP tool server** exposing tools like:

```json
{
  "tools": [
    {
      "name": "kubectl_get",
      "description": "Get Kubernetes resources",
      "inputSchema": {
        "type": "object",
        "properties": {
          "cluster_id": {"type": "string"},
          "resource": {"type": "string"},
          "namespace": {"type": "string"},
          "name": {"type": "string"},
          "output_format": {"type": "string", "enum": ["json", "yaml", "wide"]}
        }
      }
    },
    {
      "name": "kubectl_logs",
      "description": "Get pod logs",
      "inputSchema": { ... }
    }
  ]
}
```

### Recommendation for chatbot/MCP integration

Both A2A and MCP are thin wrappers around the same core: "accept a kubectl command, route it to the right cluster, return the output." The kubectlproxy service should have a **core library** that handles command routing and cluster-controller communication, with thin adapter layers for:

1. **REST API** - for direct programmatic access
2. **A2A endpoint** - for chatbot integration
3. **MCP server** - for LLM/agent tool use (e.g., kube-mcp-server)
4. **CLI/kubectl plugin** - for developer access (phase 1)

---

## Proposed Architecture Overview

```
                    ┌──────────────┐
                    │   Developer  │
                    │   kubectl    │
                    │   plugin     │
                    └──────┬───────┘
                           │
┌──────────┐    ┌──────────▼───────────┐    ┌──────────┐
│ Chatbot  │───▶│                      │◀───│ kube-mcp │
│ (A2A)    │    │    kubectlproxy      │    │  server  │
└──────────┘    │    (backend svc)     │    └──────────┘
                │                      │
                │  - Auth & RBAC       │
                │  - Command routing   │
                │  - Request/response  │
                │    correlation       │
                │  - Audit logging     │
                │  - Rate limiting     │
                └──────────┬───────────┘
                           │
            ┌──────────────┼──────────────┐
            │              │              │
            ▼              ▼              ▼
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │ cluster-ctrl │ │ cluster-ctrl │ │ cluster-ctrl │
    │ (cluster A)  │ │ (cluster B)  │ │ (cluster C)  │
    └──────────────┘ └──────────────┘ └──────────────┘
```

---

## Implementation Roadmap

### Phase 1: Kubectl action in cluster-controller (1-2 weeks)

**Cluster-controller changes:**
1. Add `ActionKubectl` type to `internal/castai/types.go`
2. Add `SendKubectlResponse()` to `CastAIClient` interface in `internal/castai/client.go`
3. Create `internal/actions/kubectl_handler.go` - reuses logic from `internal/kubectl/server.go`
4. Register in `internal/actions/actions.go`
5. Security: ensure command whitelist is enforced (reuse existing `allowedCommands` approach)

**Backend changes:**
1. New `kubectlproxy` service with REST API
2. Endpoint to submit kubectl commands → creates action for cluster-controller
3. Endpoint to receive responses from cluster-controller
4. Request-response correlation with timeout (e.g., 60s TTL in Redis)

### Phase 2: Developer CLI access (1-2 weeks)

1. kubectl plugin or custom CLI tool
2. Authenticates against backend with API key
3. Sends commands to kubectlproxy REST API
4. Displays output to terminal

### Phase 3: Chatbot & MCP integration (1-2 weeks)

1. A2A adapter on kubectlproxy for chatbot
2. MCP tool server adapter for kube-mcp integration
3. Structured tool definitions for each kubectl subcommand

### Phase 4 (optional): Lower latency with bidirectional connection

1. Add gRPC streaming or WebSocket to cluster-controller
2. Replace polling for kubectl actions with push-based delivery
3. Enable streaming use cases (`kubectl logs -f`)

---

## Security Considerations

1. **Command whitelist** - Already exists in the sidecar. Must be enforced at both backend (kubectlproxy) AND cluster-controller level.
2. **RBAC** - kubectlproxy must enforce who can run what commands on which clusters. Consider mapping backend user roles to allowed kubectl subcommands and namespaces.
3. **Audit logging** - Every command execution should be logged with: who, what, which cluster, when.
4. **Output size limits** - kubectl output can be very large (e.g., `get pods -A`). Set max response size (e.g., 10MB).
5. **Rate limiting** - Prevent abuse. Per-user, per-cluster rate limits.
6. **No write operations in v1** - Start read-only (`get`, `logs`, `describe`, `events`, `top`). Add `debug` carefully later since it creates ephemeral containers.
7. **Namespace restrictions** - Consider allowing backend to restrict which namespaces are accessible per user/role.
8. **Timeout enforcement** - Already exists (30s default). Make sure kubectlproxy also has an overall request timeout.

---

## Key Questions to Decide

1. **Polling latency (5s) acceptable for v1?** If yes, Approach A is simplest. If not, need bidirectional connection (Approach B).
2. **Should `kubectl debug` be supported in v1?** It creates ephemeral containers, which is a write operation - higher risk.
3. **Full kubectl compatibility or custom CLI?** Full K8s API proxy is a large project; custom CLI/plugin is much simpler.
4. **Which protocol first: A2A or MCP?** Depends on whether chatbot or LLM agent integration is higher priority.
5. **Multi-tenant RBAC model?** How do backend users map to cluster access permissions?

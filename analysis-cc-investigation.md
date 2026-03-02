# Cluster Controller - Comprehensive Codebase Investigation

## 1. PROJECT STRUCTURE

### Root Directory Layout
```
cluster-controller/
├── cmd/                          # Command entrypoints
│   ├── controller/              # Main controller command
│   ├── monitor/                 # Monitor/diagnostics command
│   ├── sidecar/                 # Kubectl sidecar command
│   ├── testserver/              # Test server command
│   ├── utils/                   # CLI utilities
│   └── root.go                  # Root CLI command
├── internal/                     # Core packages
│   ├── actions/                 # Action handlers (14+ handlers)
│   ├── castai/                  # CASTAI backend communication
│   ├── config/                  # Configuration management
│   ├── controller/              # Main controller service
│   ├── helm/                    # Helm chart operations
│   ├── informer/                # Kubernetes resource informers
│   ├── k8s/                     # Kubernetes utilities
│   ├── kubectl/                 # Kubectl HTTP proxy server
│   ├── logger/                  # Logging utilities
│   ├── metrics/                 # Metrics collection
│   ├── monitor/                 # Health monitoring
│   ├── nodes/                   # Node draining utilities
│   ├── volume/                  # Volume attachment handling
│   └── waitext/                 # Retry/backoff utilities
├── health/                       # Health check handlers
├── loadtest/                     # Load testing suite
├── main.go                       # Entry point
├── Dockerfile                    # Container image
├── Dockerfile.sidecar           # Sidecar container image
├── go.mod, go.sum              # Dependencies
└── Makefile                      # Build targets
```

### Go Dependencies (Key)
- **gRPC/HTTP**: `google.golang.org/grpc`, `golang.org/x/net/http2`, `github.com/go-resty/resty/v2`
- **Kubernetes**: `k8s.io/client-go`, `k8s.io/api`, `sigs.k8s.io/controller-runtime`
- **Helm**: `helm.sh/helm/v3`
- **CLI**: `github.com/spf13/cobra`, `github.com/spf13/viper`
- **Logging**: `github.com/sirupsen/logrus`, `github.com/bombsimon/logrusr/v4`
- **Metrics**: `github.com/prometheus/client_golang`
- **Utils**: `github.com/samber/lo`, `github.com/cenkalti/backoff/v4`, `github.com/google/uuid`

---

## 2. COMMUNICATION PATTERNS

### Backend Communication (CASTAI)

#### Protocol: REST API with HTTP/2
- **Base Client**: `resty.Client` (net/http wrapper)
- **API Endpoint**: Set via `API_URL` environment variable
- **Authentication**: `X-API-Key` header
- **User-Agent**: `castai-cluster-controller/VERSION`

#### HTTP Transport Configuration
```go
// File: internal/castai/client.go
- TLS 1.2+ minimum
- HTTP/2 enabled with custom timeouts
- ReadIdleTimeout: 30s (ping after idle)
- PingTimeout: 15s (close connection if no pong)
- Max 100 idle connections per host
- Custom CA certificate support via TLS.CACert config
```

#### API Endpoints and Methods

**1. Get Actions (Polling)**
- `GET /v1/kubernetes/clusters/{clusterId}/actions`
- Header: `X-K8s-Version` (Kubernetes version)
- Returns: `GetClusterActionsResponse` with array of `ClusterAction`
- Pattern: Long polling with configurable wait intervals
- Default interval: 5 seconds
- Max timeout: 5 minutes

**2. Acknowledge Action**
- `POST /v1/kubernetes/clusters/{clusterId}/actions/{actionId}/ack`
- Body: `AckClusterActionRequest` (error message if failed)
- Pattern: Retry up to 3 times with 1-second wait between retries
- Timeout per attempt: 30 seconds

**3. Send Logs**
- `POST /v1/kubernetes/clusters/{clusterId}/actions/logs`
- Body: `LogEntry` with level, time, message, fields
- Pattern: Event-based (logs exported whenever actions emit events)

**4. Send Metrics**
- `POST /v1/clusters/{clusterId}/components/{componentName}/metrics`
- Body: Prometheus format metrics (timeseries with labels and samples)
- Interval: Configurable (default via `METRICS_EXPORT_INTERVAL`)
- Component: Always "cluster-controller"

### Action Flow (Polling-Based)
```
Controller Service Loop (5s interval):
  1. GetActions() → retrieve pending actions
  2. For each action:
     - Dispatch to handler goroutine
     - Execute action with timeout protection
     - AckAction() → confirm completion
  3. Garbage collect completed actions
```

### Message Types - ClusterAction Union

The `ClusterAction` struct is a tagged union containing:
- `ActionDeleteNode` - Node deletion
- `ActionDrainNode` - Node drain with pod eviction
- `ActionPatchNode` - Node labels/taints/annotations update
- `ActionCreateEvent` - Create Kubernetes event
- `ActionChartUpsert` - Helm install/upgrade chart
- `ActionChartUninstall` - Helm uninstall
- `ActionChartRollback` - Helm rollback to version
- `ActionDisconnectCluster` - Graceful shutdown
- `ActionCheckNodeDeleted` - Verify node deletion
- `ActionCheckNodeStatus` - Check node readiness status
- `ActionEvictPod` - Force evict pod
- `ActionPatch` - Generic Kubernetes resource patch
- `ActionCreate` - Generic Kubernetes resource create
- `ActionDelete` - Generic Kubernetes resource delete

---

## 3. EXISTING FUNCTIONALITIES

### Action Handlers (24 handlers)

**Node Management** (4 handlers):
1. `DeleteNodeHandler` - Delete Kubernetes node, terminate pods, clean VolumeAttachments
2. `DrainNodeHandler` - Cordon node, evict pods, wait for termination
3. `PatchNodeHandler` - Update node labels, taints, annotations, capacity
4. `CheckNodeStatusHandler` / `CheckNodeStatusInformerHandler` - Verify node state (ready/deleted)
5. `CheckNodeDeletedHandler` - Verify node deletion

**Pod Management** (2 handlers):
6. `EvictPodHandler` - Force evict single pod with timeout
7. (Drain handler includes pod deletion/eviction logic)

**Kubernetes Resource Management** (3 handlers):
8. `CreateHandler` - Generic resource creation
9. `PatchHandler` - Generic resource patching
10. `DeleteHandler` - Generic resource deletion

**Chart/Helm Management** (3 handlers):
11. `ChartUpsertHandler` - Install/upgrade Helm charts
12. `ChartUninstallHandler` - Uninstall Helm releases
13. `ChartRollbackHandler` - Rollback Helm release to version

**Event Management** (1 handler):
14. `CreateEventHandler` - Create Kubernetes events

**Cluster Management** (1 handler):
15. `DisconnectClusterHandler` - Graceful shutdown

### Core Features

**1. Action Execution**
- File: `internal/controller/controller.go`
- Concurrent execution with goroutines (limited by `MaxActionsInProgress`)
- Panic recovery with stack trace logging
- Metrics collection (start/finish with duration)
- Action deduplication (prevents re-execution of completed actions)

**2. Node Draining**
- File: `internal/actions/drain_node_handler.go`
- Pod eviction using Kubernetes eviction API
- Graceful shutdown with 2-minute timeout
- Retry logic (5 attempts with 5-second delays)
- Skip CAST AI control plane pods
- Volume attachment wait support (configurable)

**3. Volume Attachment Wait**
- File: `internal/volume/detachment_waiter.go`
- Waits for VolumeAttachments to detach after node drain
- Configurable per-action (default 60s from `DRAIN_VOLUME_DETACH_TIMEOUT`)
- Indexed lookup via informer
- Excludes DaemonSet/static pods from wait

**4. Node Informer**
- Watches Kubernetes nodes with caching
- Optional pod informer for pod status tracking
- VolumeAttachment informer with RBAC permission checking
- Configurable cache sync timeout
- Custom indexers for efficient queries

**5. Health Checks**
- HTTP endpoint: `:8090/healthz` (pprof port)
- Initialization, polling, and leader election health tracking
- Request timeout monitoring

**6. Leader Election**
- Lease-based with ConfigMap
- Configurable lease duration and renew deadline
- Single-instance or multi-instance deployment support

**7. Kubectl Sidecar**
- HTTP server: `127.0.0.1:7070` (default)
- Whitelist-based command execution
- Allowed commands: `get`, `logs`, `describe`, `events`, `top`
- Timeout per command: 30 seconds (configurable)
- Port configurable via `KUBECTL_PORT`

**8. Metrics & Logging**
- Prometheus metrics collection
- Custom metrics registration
- Log exporter to CASTAI API
- Structured logging with logrus
- Multiple log levels

**9. CSR Approval (GKE)**
- Auto-approval of Certificate Signing Requests on GKE
- File: `internal/actions/csr/`
- Disabled if `AUTOSCALING_DISABLED=true`

**10. Monitoring Mode**
- Separate `monitor` command
- Watches controller metadata file for restarts
- Reports pod and node events on unexpected restart
- Helps diagnose OOMKill and other failures

---

## 4. CONFIGURATION

### Environment Variables (Required)
- `API_KEY` - CASTAI API authentication key
- `API_URL` - CASTAI API endpoint URL
- `CLUSTER_ID` - Unique cluster identifier

### Environment Variables (Optional)

**Logging**
- `LOG_LEVEL` - Log level (default varies)

**Kubernetes**
- `KUBECONFIG` - Path to kubeconfig file

**Network**
- `TLS_CA_CERT` - Custom CA certificate (PEM format)

**Performance**
- `KUBE_CLIENT_QPS` - Kubernetes API rate limit queries/sec (default: varies)
- `KUBE_CLIENT_BURST` - Burst allowance for K8s client
- `MAX_ACTIONS_IN_PROGRESS` - Concurrent action limit

**Timeouts**
- `INFORMER_CACHE_SYNC_TIMEOUT` - Informer cache startup timeout (default: 1m)
- `DRAIN_VOLUME_DETACH_TIMEOUT` - Volume detach wait timeout (default: 60s)

**Sidecar (if using sidecar)**
- `KUBECTL_PORT` - HTTP server port (default: 7070)
- `KUBECTL_TIMEOUT` - Command execution timeout (default: 30s)
- `KUBECTL_ALLOWED_COMMANDS` - CSV list of allowed commands

**Leader Election**
- `LEADER_ELECTION_ENABLED` - Enable multi-instance deployment (default: false)
- `LEADER_ELECTION_LOCK_NAME` - Lease name
- `LEADER_ELECTION_LEASE_DURATION` - Lease duration
- `LEADER_ELECTION_LEASE_RENEW_DEADLINE` - Renew deadline

**Drain Options**
- `DRAIN_DISABLE_VOLUME_DETACH_WAIT` - Disable volume wait feature
- `VOLUME_ATTACHMENT_DEFAULT_TIMEOUT` - Default VA wait timeout

**Monitoring**
- `MONITOR_METADATA` - Path to metadata file for diagnostics
- `SELF_POD_NAMESPACE` - Controller pod namespace
- `SELF_POD_NAME` - Controller pod name
- `SELF_POD_NODE` - Controller pod node name

**Metrics Export**
- `METRICS_EXPORT_ENABLED` - Enable Prometheus metrics export
- `METRICS_EXPORT_INTERVAL` - Export interval
- `METRICS_PORT` - Prometheus metrics port (default: 8080)
- `PPROF_PORT` - Go pprof profiling port (default: 8090)

### Config Struct (internal/config/config.go)
```go
type Config struct {
    Log              Log
    API              API
    TLS              TLS
    Kubeconfig       string
    KubeClient       KubeClient       // QPS, Burst
    ClusterID        string
    PprofPort        int
    Metrics          Metrics          // Port, ExportEnabled, ExportInterval
    LeaderElection   LeaderElection   // Enabled, LockName, timings
    Drain            Drain            // DisableVolumeDetachWait
    VolumeAttachment VolumeAttachment // DefaultTimeout
    Informer         Informer         // EnablePod, EnableNode, CacheSyncTimeout
    AutoscalingDisabled bool
    MaxActionsInProgress int
    MonitorMetadataPath string
    SelfPod             Pod              // Namespace, Name, Node
}
```

---

## 5. ARCHITECTURE

### Dependency Injection Pattern

**Controller Setup (cmd/controller/run.go)**
```
1. Create CASTAI REST client (RestyClient with TLS config)
2. Create CASTAI Client wrapper (auth headers, path building)
3. Create Kubernetes clientset(s) with rate limiting
4. Create Helm client with discovery mapper
5. Create Informer Manager (nodes, pods, VolumeAttachments)
6. Create Action Handlers (injected with K8s clients)
7. Create Controller Service (main polling loop)
8. Setup Leader Election (if enabled)
9. Start HTTP servers (health, pprof, metrics)
```

**Key Clients Injected**
- `castai.CastAIClient` - Backend communication
- `kubernetes.Interface` - Kubernetes API (main, leader, dynamic)
- `dynamic.Interface` - Generic resource operations
- `helm.Client` - Helm operations
- `informer.Manager` - Kubernetes resource watching
- `volume.DetachmentWaiter` - Volume attachment tracking

### Main Service Loop (Controller.Run)

File: `internal/controller/controller.go`

```
1. PollWaitInterval loop (default 5s)
2. Call GetActions() with K8s version header
3. For each action:
   - Check: Already started? Recently completed? At capacity?
   - Dispatch: Spawn goroutine per action
   - Execute handler.Handle(ctx, action)
   - Catch panics, record metrics
   - AckAction() with error (retry 3x with 1s backoff)
4. Garbage collect recently completed actions (after 2 visits)
5. Track health check status
```

### Lifecycle Management

**Startup**
1. Signal handler setup
2. Version injection
3. Root CLI command dispatch
4. Config loading from environment
5. Kubernetes client initialization
6. Informer cache sync with timeout
7. HTTP servers start
8. Leader election (if enabled)
9. Main polling loop starts

**Shutdown**
1. Context cancellation
2. Graceful wait for in-flight actions
3. Leader lease release (if leader)
4. HTTP servers close
5. Informer stop/shutdown
6. Resource cleanup

### Component Wiring

**Informer Manager**
```
- Shared factory pattern (Kubernetes client-go)
- Lazy initialization of node/pod/VA informers
- Handles cache sync and startup blocking
- Provides listers and indexers for queries
- Option-based configuration
```

**Action Handlers**
```
- Map of reflect.Type → ActionHandler
- NewDefaultActionHandlers() builds complete map
- Each handler is a singleton with shared K8s client
- Handlers implement ActionHandler interface (Handle method)
```

**Health Provider**
```
- Tracks initialization phase
- Monitors polling health (action request success)
- Leader election adapter for multi-instance
- Endpoint: GET /healthz
```

**Log Exporter**
```
- Wraps logger with custom hook
- Sends structured log entries to CASTAI API
- Async (non-blocking)
- Configurable log level threshold
```

**Metric Exporter**
```
- Collects Prometheus metrics from registry
- Sends to CASTAI metrics endpoint
- Interval-based (configurable)
- Converts Prometheus format to CASTAI timeseries format
```

### Error Handling & Recovery

**Action Execution**
- Panic recovery with stack trace
- Error wrapped with action type
- Error sent to backend via AckAction
- Failed action not marked as completed (retry-able)

**API Requests**
- Retry logic: `waitext.Retry()` with backoff
- Exponential or constant backoff strategies
- Configurable retry counts
- Context cancellation propagation

**Network Failures**
- HTTP/2 ping timeout (15s) closes bad connections
- Backoff prevents connection storms
- ReadIdleTimeout (30s) detects stale connections

**Kubernetes Operations**
- FieldSelector, label selectors for efficient queries
- Precondition checks (UID) for safe updates
- Resource not found errors handled gracefully
- Retries for transient errors

### Plugin Points / Extension

**Action Handlers**
- Implement `ActionHandler` interface
- Register in `ActionHandlers` map
- Add corresponding action type to `castai.ClusterAction`

**Informers**
- Options-based enabling (EnableNodeInformer, EnablePodInformer)
- Custom indexers via `WithNodeIndexers()`
- VA permission errors gracefully handled

**Metrics**
- Prometheus registry extensible
- Custom metrics via metrics.RegisterCustomMetrics()
- Export interval configurable

**TLS/Security**
- Custom CA certificate support
- TLS 1.2+ enforced
- Bearer token via X-API-Key header

---

## 6. DETAILED COMPONENT BREAKDOWN

### A. CASTAI Communication Layer (internal/castai/)

**File: client.go**
- `CastAIClient` interface (4 methods)
- `Client` struct with resty.Client
- `NewRestyClient()` - HTTP/2 transport setup
- `createHTTPTransport()` - TLS, proxy, timeouts
- `createTLSConfig()` - Custom CA cert handling
- Prometheus metrics conversion functions

**Types: types.go**
- `ClusterAction` - 13 action type fields
- `GetClusterActionsResponse` - Server response
- `AckClusterActionRequest` - Error reporting
- All action payload structures
- Prometheus metric structures
- Validation helpers

### B. Action Handlers (internal/actions/) - 15 Handlers

**Generic Handlers**
- `CreateHandler` - Dynamic resource creation
- `PatchHandler` - Two-way merge patch
- `DeleteHandler` - Resource deletion

**Node Handlers**
- `DeleteNodeHandler` - Force delete node, pods, VAs
- `DrainNodeHandler` - Cordon, evict, wait
- `PatchNodeHandler` - Update node metadata
- `CheckNodeStatusHandler` - Poll node state
- `CheckNodeStatusInformerHandler` - Watch node state
- `CheckNodeDeletedHandler` - Verify deletion

**Pod Handlers**
- `EvictPodHandler` - Kubernetes eviction API

**Chart Handlers**
- `ChartUpsertHandler` - Helm install/upgrade
- `ChartUninstallHandler` - Helm uninstall
- `ChartRollbackHandler` - Helm rollback

**Event Handlers**
- `CreateEventHandler` - Create K8s events

**Cluster Handlers**
- `DisconnectClusterHandler` - Shutdown signal

### C. Controller Service (internal/controller/)

**controller.go (260+ lines)**
- `Controller` struct - Main service
- `Run()` - Infinite polling loop
- `doWork()` - Single poll iteration
- `handleActions()` - Dispatch to handlers
- `handleAction()` - Execute single action
- `ackAction()` - Send result back
- `startProcessing()` - Check preconditions
- `finishProcessing()` - Update tracking
- `gcCompletedActions()` - Memory cleanup

**logexporter/**
- Hook-based log collection
- Field-based filtering
- Async sending to API

**metricexporter/**
- Periodic metric collection
- Prometheus registry scraping
- Format conversion

### D. Kubernetes Utilities (internal/k8s/)

**kubernetes.go**
- `Client` interface
- `PatchNode()` - Strategic merge patch
- `EvictPod()` - Eviction API
- `CordonNode()` - Mark unschedulable
- `GetNodeByIDs()` - Match by name/id/provider-id
- `DeletePod()` - Force delete with grace period
- `ExecuteBatchPodActions()` - Parallel pod operations
- Backoff strategies
- Common retry helpers

### E. Informers (internal/informer/)

**manager.go**
- `Manager` - Shared informer factory wrapper
- Node, pod, VolumeAttachment informers
- Cache sync with timeout
- Permission checking for VA informer
- Lifecycle management

**node_informer.go, pod_informer.go, va_informer.go**
- Wrappers around Kubernetes informers
- Listers and indexers
- Sync status tracking

### F. Helm Client (internal/helm/)

**client.go**
- `Client` interface - Install, Upgrade, Uninstall, Rollback, GetRelease
- Helm action configuration
- REST mapper for dynamic discovery
- Chart loader integration
- Values override support
- Release history management

**chart_loader.go**
- Chart repository access
- Chart pulling and caching
- Version resolution

### G. Kubectl Sidecar (internal/kubectl/)

**server.go (100+ lines)**
- HTTP server on 127.0.0.1:7070
- POST /kubectl endpoint
- JSON request/response format
- Subcommand whitelist enforcement
- Command timeout with context
- Exit code and stdout/stderr capture
- Error response formatting

### H. Volume Attachment (internal/volume/)

**detachment_waiter.go**
- Waits for VolumeAttachments to detach
- Indexed lookup from informer
- Poll-based checking
- Configurable timeout
- Pod exclusion support
- Detailed error reporting (remaining VAs)

### I. Monitoring (cmd/monitor/, internal/monitor/)

**monitor.go**
- Watches metadata file for controller restarts
- Reports pod and node events on restart
- Helps diagnose OOMKill and crashes

---

## 7. FLOW DIAGRAMS

### Main Action Processing Flow
```
controller.Run()
├─ for loop each 5s
├─ GetActions() → API call
│  └─ X-K8s-Version header
├─ handleActions() → for each action
│  ├─ startProcessing() → check preconditions
│  └─ goroutine:
│     ├─ handleAction() → dispatch to handler
│     │  └─ handler.Handle(ctx, action)
│     ├─ catch panic, record metrics
│     └─ ackAction() → 3x retry with 1s backoff
├─ gcCompletedActions() → cleanup memory
└─ health check update
```

### Node Drain Flow
```
DrainNodeHandler.Handle()
├─ Validate input (node name, IDs)
├─ Get node by IDs
├─ Cordon node (mark unschedulable)
├─ List pods on node
├─ Separate evictable/non-evictable
├─ Evict evictable pods (with retry)
├─ Delete non-evictable pods (with grace period)
├─ Wait for pod termination
├─ Wait for VolumeAttachment detach (optional)
└─ Return success/error
```

### Kubernetes API Call Pattern
```
action handler
├─ Create logger with action ID
├─ Validate input
├─ Loop with retry backoff:
│  ├─ Make K8s API call
│  ├─ Check response
│  └─ Retry on transient error
├─ Log result
└─ Return error or nil
```

---

## 8. SUMMARY TABLE

| Aspect | Implementation |
|--------|-----------------|
| **Communication** | REST API over HTTP/2, TLS 1.2+, polling-based |
| **Architecture** | Dependency injection, interface-based design |
| **Concurrency** | Goroutine per action, waitgroup tracking |
| **Retry Logic** | Exponential/constant backoff, configurable |
| **State Management** | In-memory maps, GC-based cleanup |
| **Kubernetes Interaction** | Client-go with informers, strategic merge patches |
| **Helm Integration** | Helm v3 action API with chart loader |
| **Health Monitoring** | HTTP health endpoint, leader election heartbeat |
| **Metrics** | Prometheus format, exported to CASTAI API |
| **Logging** | Structured logrus, sent to API |
| **Secrets** | API key in header, CA cert in config |
| **Extensibility** | Action handler interface, informer options |

---

## 9. KEY FILES BY FUNCTION

**Communication**
- `internal/castai/client.go` - REST client
- `cmd/controller/run.go` - Controller setup

**Action Execution**
- `internal/controller/controller.go` - Main loop
- `internal/actions/actions.go` - Handler registry

**Node Operations**
- `internal/actions/drain_node_handler.go` - Drain logic
- `internal/actions/delete_node_handler.go` - Delete logic
- `internal/k8s/kubernetes.go` - Pod operations

**Volume Management**
- `internal/volume/detachment_waiter.go` - Wait logic

**Kubernetes Integration**
- `internal/informer/manager.go` - Informer setup
- `internal/k8s/kubernetes.go` - K8s utilities

**Sidecar**
- `cmd/sidecar/main.go` - Entry point
- `internal/kubectl/server.go` - HTTP server

**Configuration**
- `internal/config/config.go` - Config structure

**Health & Monitoring**
- `health/healthz.go` - Health checks
- `internal/monitor/monitor.go` - Restart detection

---

## 10. CRITICAL OBSERVATIONS

1. **Polling-Based**: Not event-driven; 5-second poll interval means 5s latency
2. **In-Memory State**: No persistent state store; restart loses action tracking
3. **Garbage Collection**: Recently completed actions tracked in memory to prevent re-execution
4. **Action Deduplication**: 2-visit GC strategy to allow time for duplicate detection
5. **Concurrent Limits**: `MaxActionsInProgress` prevents goroutine explosion
6. **Panic Safety**: All handlers wrapped with panic recovery
7. **TLS Enforced**: Minimum TLS 1.2, custom CA support
8. **Kubernetes Native**: Heavy use of Kubernetes client-go patterns
9. **Optional Components**: Pod/node informers can be disabled
10. **Leader Election**: Multi-instance support via lease-based election


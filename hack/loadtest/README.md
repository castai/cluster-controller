# CAST AI Load Testing Environment

This directory contains a complete Helm-based load testing environment for CAST AI Cluster Controller with observability stack.

## Components

The load testing environment includes:

- **KWOK**: Simulates fake Kubernetes nodes for load testing
- **Cluster Controller**: CAST AI cluster controller (optional)
- **Load Test Agent**: Test server that simulates CAST AI API
- **Prometheus**: Metrics collection and storage
- **Grafana**: Visualization and dashboards
- **Loki**: Log aggregation
- **Promtail**: Log shipping from pods
- **kube-state-metrics**: Kubernetes cluster metrics

## Architecture

All components are deployed using Helm charts:
- Official Helm charts are used for Prometheus, Grafana, Loki, Promtail, and kube-state-metrics
- Custom Helm chart for the load test agent
- Dependencies are managed through Helm chart dependencies

## Quick Start

### Using Makefile (Recommended)

From the **repository root**:

```bash
# Build, push, and deploy everything
make deploy-loadtest VERSION=v1.2.3

# Check status
make loadtest-status

# Access Grafana
make loadtest-forward-grafana

# Clean up
make undeploy-loadtest
```

See [MAKEFILE_USAGE.md](MAKEFILE_USAGE.md) for complete Makefile documentation.

### Using Shell Scripts Directly

From this directory:

```bash
# Deploy with defaults
./deploy-helm.sh

# Deploy with custom values
./deploy-helm.sh my-values.yaml

# Deploy with separate cluster-controller values
CC_VALUES_FILE=cc-values.yaml ./deploy-helm.sh loadtest-values.yaml
```

See [USAGE.md](USAGE.md) for complete shell script usage.

### Environment Variables

You can customize the deployment using environment variables:

```bash
# Image configuration
export LOADTEST_IMAGE_REPOSITORY="us-docker.pkg.dev/castai-hub/library/cluster-controller"
export LOADTEST_IMAGE_TAG="latest"
export IMAGE_REPOSITORY="us-docker.pkg.dev/castai-hub/library/cluster-controller"
export IMAGE_TAG="latest"

# Deployment configuration
export DEPLOY_CLUSTER_CONTROLLER="true"  # Set to "false" to skip cluster controller
export KWOK_REPLICAS="10"                # Number of fake nodes
export NAMESPACE="castai-agent"          # Kubernetes namespace
export RELEASE_NAME="castai-loadtest"   # Helm release name

./deploy-helm.sh
```

## Accessing Services

### Grafana

```bash
kubectl port-forward -n castai-agent svc/castai-loadtest-grafana 3000:80
```

Then open http://localhost:3000

Grafana is pre-configured with:
- Anonymous authentication (no login required)
- Prometheus datasource
- Loki datasource
- Cluster Controller dashboard

### Prometheus

```bash
kubectl port-forward -n castai-agent svc/castai-loadtest-prometheus-server 9090:9090
```

Then open http://localhost:9090

### Load Test Agent

```bash
kubectl port-forward -n castai-agent svc/castai-loadtest-agent-service 8080:8080
```

Then open http://localhost:8080

## Uninstall

To uninstall all components:

```bash
./undeploy-helm.sh
```

To completely remove the namespace:

```bash
kubectl delete namespace castai-agent
```

## Chart Structure

```
chart/
├── Chart.yaml              # Chart metadata and dependencies
├── values.yaml             # Default configuration values
├── dashboards/             # Grafana dashboard JSON files
│   └── cluster-controller-dashboard.json
└── templates/              # Kubernetes resource templates
    ├── _helpers.tpl
    ├── clusterrolebinding.yaml
    ├── dashboard-configmap.yaml
    ├── deployment.yaml
    ├── service.yaml
    └── serviceaccount.yaml
```

## Customization

### Modifying Values

Edit `chart/values.yaml` or create a custom values file:

```yaml
# Example: Increase Prometheus resources
prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
        cpu: "8"

# Example: Disable a component
loki:
  enabled: false
```

### Adding Dashboards

1. Add your dashboard JSON file to `chart/dashboards/`
2. Update `chart/templates/dashboard-configmap.yaml` to include it
3. Redeploy with `./deploy-helm.sh`

## Troubleshooting

### Common Errors

#### FlowSchema Error (kwok)
```
Error: creation or update of FlowSchema object kwok-controller that references
the exempt PriorityLevelConfiguration is not allowed
```

**Fixed in current version.** The kwok deployment uses:
- `kwok-values.yaml` - Disables PriorityClass creation
- `kwok-filter.sh` - Post-renderer that filters out FlowSchema and PriorityClass resources

These resources are optional for kwok and not needed for load testing. The filter automatically removes them during deployment without affecting kwok functionality.

#### Helm Dependencies Not Found
```
Error: found in Chart.yaml, but missing in charts/ directory
```

**Solution:** Build dependencies first:
```bash
make loadtest-helm-deps
# or
cd hack/loadtest/chart && helm dependency build
```

#### Image Pull Errors
```
Error: Failed to pull image
```

**Solution:** Ensure image was built and pushed:
```bash
# Check if image exists
docker images | grep cluster-controller

# Build and push
make release VERSION=v1.0.0

# Then deploy
make deploy-loadtest-only VERSION=v1.0.0
```

#### Namespace Already Exists
```
Error: namespaces "castai-agent" already exists
```

**Solution:** This is a warning, not an error. Helm will use the existing namespace.

### Check Pod Status

```bash
kubectl get pods -n castai-agent
```

### View Logs

```bash
# Loadtest agent
kubectl logs -n castai-agent -l app=castai-loadtest-agent

# Cluster controller
kubectl logs -n castai-agent -l app.kubernetes.io/name=castai-cluster-controller

# Prometheus
kubectl logs -n castai-agent -l app.kubernetes.io/name=prometheus

# Grafana
kubectl logs -n castai-agent -l app.kubernetes.io/name=grafana

# Kwok controller (if issues with fake nodes)
kubectl logs -n castai-agent -l app=kwok-controller
```

### Verify Helm Releases

```bash
helm list -n castai-agent
```

### Check Helm Values

```bash
# See what values are being used
helm get values cluster-controller -n castai-agent
helm get values castai-loadtest -n castai-agent
```

### Debug Helm Chart

```bash
# Test chart rendering
helm template castai-loadtest ./chart

# Debug installation
helm install castai-loadtest ./chart --dry-run --debug

# See all resources that will be created
helm template castai-loadtest ./chart | kubectl apply --dry-run=client -f -
```

## Migration from Old Setup

The new Helm-based setup replaces:
- `loadtest-components.yaml` → Helm templates in `chart/templates/`
- `kustomization.yaml` → No longer needed (Helm handles templating)
- `deploy.sh` → `deploy-helm.sh` (uses Helm instead of kubectl apply)
- Grafana config files → Integrated into Helm values

## Development

### Building Dependencies

```bash
cd chart
helm dependency build
```

### Testing Locally

```bash
# Render templates
helm template castai-loadtest ./chart

# Install to a test namespace
helm install castai-loadtest ./chart -n test --create-namespace
```

### Updating Dependencies

Edit `chart/Chart.yaml` to update dependency versions, then:

```bash
cd chart
helm dependency update
```

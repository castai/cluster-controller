# Usage Guide: deploy-helm.sh

## Synopsis

```
./deploy-helm.sh [--kwok-values FILE] [--cc-values FILE] [--loadtest-values FILE]
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--kwok-values FILE` | `./kwok-values.yaml` | Path to kwok Helm values file |
| `--cc-values FILE` | (none) | Path to cluster-controller Helm values file |
| `--loadtest-values FILE` | (none) | Path to loadtest Helm values file |

## Examples

### Deploy with defaults

```bash
./deploy-helm.sh
```

### Custom kwok node count

Create `my-kwok-values.yaml`:
```yaml
replicas: 100
priorityClass:
  enabled: false
```

```bash
./deploy-helm.sh --kwok-values my-kwok-values.yaml
```

### Custom cluster controller values

Create `cc-values.yaml`:
```yaml
resources:
  requests:
    memory: "2Gi"
    cpu: "1000m"
  limits:
    memory: "4Gi"
    cpu: "2000m"

replicaCount: 2
```

```bash
./deploy-helm.sh --cc-values cc-values.yaml
```

### Custom loadtest agent / observability values

Create `loadtest-values.yaml`:
```yaml
loadtestAgent:
  image:
    repository: "my-registry/cluster-controller"
    tag: "feature-branch"

prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
        cpu: "8"
```

```bash
./deploy-helm.sh --loadtest-values loadtest-values.yaml
```

### All three at once

```bash
./deploy-helm.sh \
  --kwok-values my-kwok-values.yaml \
  --cc-values cc-values.yaml \
  --loadtest-values loadtest-values.yaml
```

### Production-like setup

```bash
./deploy-helm.sh \
  --kwok-values kwok-prod.yaml \
  --cc-values cc-prod.yaml \
  --loadtest-values loadtest-prod.yaml
```

## Environment Variables

The following variables are still used for configuration that does not correspond to a values file:

| Variable | Default | Description |
|----------|---------|-------------|
| `IMAGE_REPOSITORY` | `us-docker.pkg.dev/castai-hub/library/cluster-controller` | Cluster controller image repository |
| `IMAGE_TAG` | `latest` | Cluster controller image tag |
| `LOADTEST_IMAGE_REPOSITORY` | `$IMAGE_REPOSITORY` | Loadtest agent image repository |
| `LOADTEST_IMAGE_TAG` | `$IMAGE_TAG` | Loadtest agent image tag |
| `DEPLOY_CLUSTER_CONTROLLER` | `true` | Whether to deploy cluster controller |
| `NAMESPACE` | `castai-agent` | Kubernetes namespace |
| `RELEASE_NAME` | `castai-loadtest` | Helm release name for loadtest stack |
| `LOADTEST_CHART_PATH` | `./chart` | Path to loadtest Helm chart |

## Verification

After deployment, verify the configuration:

```bash
# Check cluster-controller values
helm get values cluster-controller -n castai-agent

# Check loadtest stack values
helm get values castai-loadtest -n castai-agent

# Check running pods
kubectl get pods -n castai-agent
```

## Upgrade Existing Deployment

```bash
# Upgrade with updated values
./deploy-helm.sh \
  --cc-values cc-updated.yaml \
  --loadtest-values loadtest-updated.yaml
```

## Troubleshooting

```bash
# Dry-run to preview what would be deployed
helm upgrade cluster-controller castai-helm/castai-cluster-controller \
  -n castai-agent \
  -f cc-values.yaml \
  --dry-run --debug

# See current configuration
helm get values cluster-controller -n castai-agent --all
helm get values castai-loadtest -n castai-agent --all
```

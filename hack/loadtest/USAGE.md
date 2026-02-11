# Usage Guide: Passing Values to Cluster Controller

## Overview

The `deploy-helm.sh` script supports multiple ways to configure the cluster-controller deployment.

## Methods

### 1. Environment Variable (Recommended for Separate Values Files)

```bash
# Pass cluster-controller values via environment variable
CC_VALUES_FILE=cluster-controller-values.yaml ./deploy-helm.sh

# Pass both cluster-controller and loadtest values
CC_VALUES_FILE=cc-values.yaml ./deploy-helm.sh loadtest-values.yaml
```

### 2. Command Line Argument (Loadtest Only)

```bash
# Pass loadtest values as first argument
./deploy-helm.sh loadtest-values.yaml
```

### 3. Environment Variables for Specific Settings

```bash
# Image configuration
export IMAGE_REPOSITORY="us-docker.pkg.dev/castai-hub/library/cluster-controller"
export IMAGE_TAG="v1.2.3"

# Deployment options
export DEPLOY_CLUSTER_CONTROLLER="true"
export KWOK_REPLICAS="50"
export NAMESPACE="castai-agent"

./deploy-helm.sh
```

### 4. Direct Helm Commands (Advanced)

```bash
# Deploy loadtest stack first
./deploy-helm.sh

# Then manually upgrade cluster-controller with custom values
helm upgrade cluster-controller castai-helm/castai-cluster-controller \
  -n castai-agent \
  -f my-cluster-controller-values.yaml
```

## Examples

### Example 1: Custom Cluster Controller Resources

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

podLabels:
  environment: loadtest
  team: platform
```

Deploy:
```bash
CC_VALUES_FILE=cc-values.yaml ./deploy-helm.sh
```

### Example 2: Custom Images for Both

Create `cc-values.yaml`:
```yaml
image:
  repository: "my-registry/cluster-controller"
  tag: "feature-branch"
  pullPolicy: Always
```

Create `loadtest-values.yaml`:
```yaml
loadtestAgent:
  image:
    repository: "my-registry/cluster-controller"
    tag: "feature-branch"
```

Deploy:
```bash
CC_VALUES_FILE=cc-values.yaml ./deploy-helm.sh loadtest-values.yaml
```

### Example 3: Environment Variables Only

```bash
# Quick image update without values files
export IMAGE_TAG="v1.2.3"
export LOADTEST_IMAGE_TAG="v1.2.3"
./deploy-helm.sh
```

### Example 4: Disable Cluster Controller

```bash
# Deploy only the loadtest observability stack
export DEPLOY_CLUSTER_CONTROLLER="false"
./deploy-helm.sh
```

### Example 5: Scale Up KWOK Nodes

```bash
# Create more fake nodes for heavier load testing
export KWOK_REPLICAS="100"
./deploy-helm.sh
```

### Example 6: Custom Prometheus Resources in Loadtest Stack

Create `loadtest-values.yaml`:
```yaml
prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
        cpu: "8"

grafana:
  resources:
    requests:
      memory: "8Gi"
      cpu: "4"

loadtestAgent:
  enabled: true
```

Deploy:
```bash
./deploy-helm.sh loadtest-values.yaml
```

## Complete Example: Production-like Setup

```bash
# 1. Create cluster-controller values
cat > cc-prod.yaml <<EOF
image:
  repository: "us-docker.pkg.dev/castai-hub/library/cluster-controller"
  tag: "v2.1.0"
  pullPolicy: Always

resources:
  requests:
    memory: "4Gi"
    cpu: "2000m"
  limits:
    memory: "8Gi"
    cpu: "4000m"

replicaCount: 3

autoscaling:
  enabled: true

podLabels:
  environment: loadtest
  version: v2.1.0
EOF

# 2. Create loadtest values
cat > loadtest-prod.yaml <<EOF
loadtestAgent:
  image:
    repository: "us-docker.pkg.dev/castai-hub/library/cluster-controller"
    tag: "v2.1.0"
  resources:
    requests:
      memory: "2Gi"
      cpu: "1000m"

prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
        cpu: "8"
    retention: "30d"

grafana:
  resources:
    requests:
      memory: "8Gi"
      cpu: "4"

loki:
  loki:
    limits_config:
      retention_period: 720h  # 30 days
EOF

# 3. Deploy with custom KWOK scale
export KWOK_REPLICAS="200"
CC_VALUES_FILE=cc-prod.yaml ./deploy-helm.sh loadtest-prod.yaml
```

## Environment Variables Reference

| Variable | Default | Description |
|----------|---------|-------------|
| `CC_VALUES_FILE` | (none) | Path to cluster-controller values file |
| `LOADTEST_VALUES_FILE` | $1 | Path to loadtest values file (or first argument) |
| `IMAGE_REPOSITORY` | us-docker.pkg.dev/castai-hub/library/cluster-controller | Cluster controller image repository |
| `IMAGE_TAG` | latest | Cluster controller image tag |
| `LOADTEST_IMAGE_REPOSITORY` | $IMAGE_REPOSITORY | Loadtest agent image repository |
| `LOADTEST_IMAGE_TAG` | $IMAGE_TAG | Loadtest agent image tag |
| `DEPLOY_CLUSTER_CONTROLLER` | true | Whether to deploy cluster controller |
| `KWOK_REPLICAS` | 10 | Number of fake nodes to create |
| `NAMESPACE` | castai-agent | Kubernetes namespace |
| `RELEASE_NAME` | castai-loadtest | Helm release name for loadtest stack |
| `LOADTEST_CHART_PATH` | ./chart | Path to loadtest Helm chart |

## Verification

After deployment, verify the configuration:

```bash
# Check cluster-controller values
helm get values cluster-controller -n castai-agent

# Check loadtest stack values
helm get values castai-loadtest -n castai-agent

# Check running pods
kubectl get pods -n castai-agent

# Check cluster-controller logs
kubectl logs -n castai-agent -l app.kubernetes.io/name=castai-cluster-controller --tail=50
```

## Upgrade Existing Deployment

### Upgrade Cluster Controller Only

```bash
helm upgrade cluster-controller castai-helm/castai-cluster-controller \
  -n castai-agent \
  -f cc-updated-values.yaml
```

### Upgrade Loadtest Stack Only

```bash
helm upgrade castai-loadtest ./chart \
  -n castai-agent \
  -f loadtest-updated-values.yaml
```

### Upgrade Both

```bash
CC_VALUES_FILE=cc-updated.yaml ./deploy-helm.sh loadtest-updated.yaml
```

## Troubleshooting

### Check what values are being used:

```bash
# Dry-run to see what would be deployed
helm upgrade cluster-controller castai-helm/castai-cluster-controller \
  -n castai-agent \
  -f my-values.yaml \
  --dry-run --debug
```

### See current configuration:

```bash
helm get values cluster-controller -n castai-agent --all
```

### Reset to defaults:

```bash
# Redeploy without custom values
export DEPLOY_CLUSTER_CONTROLLER="true"
unset CC_VALUES_FILE
./deploy-helm.sh
```

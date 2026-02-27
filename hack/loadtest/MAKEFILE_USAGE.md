# Makefile Targets for Load Testing

This document describes the Makefile targets available for load testing operations.

## Quick Reference

```bash
# Build, push, and deploy everything
make deploy-loadtest VERSION=v1.2.3

# Deploy without building (use existing image)
make deploy-loadtest-only VERSION=v1.2.3

# Check deployment status
make loadtest-status

# Remove everything
make undeploy-loadtest

# Access Grafana
make loadtest-forward-grafana

# Access Prometheus
make loadtest-forward-prometheus

# View logs
make loadtest-logs
```

## Available Targets

### Deployment Targets

#### `make deploy-loadtest`
Build, push Docker image, and deploy the complete loadtest environment.

**Required variables:**
- `VERSION` - Image tag to use (e.g., `v1.2.3`, `latest`)

**Optional variables:**
- `DOCKER_REPOSITORY` - Docker repository (default: `us-docker.pkg.dev/castai-hub/library/cluster-controller`)
- `KWOK_VALUES_FILE` - Path to kwok Helm values file
- `CC_VALUES_FILE` - Path to cluster-controller Helm values file
- `LOADTEST_VALUES_FILE` - Path to loadtest Helm values file
- `ARCH` - Architecture to build for (default: auto-detected)

**Example:**
```bash
# Basic deployment
make deploy-loadtest VERSION=v1.2.3

# With custom loadtest values
make deploy-loadtest VERSION=v1.2.3 LOADTEST_VALUES_FILE=my-values.yaml

# With all three values files
make deploy-loadtest VERSION=v1.2.3 KWOK_VALUES_FILE=kwok.yaml CC_VALUES_FILE=cc.yaml LOADTEST_VALUES_FILE=lt.yaml

# With custom repository
make deploy-loadtest VERSION=latest DOCKER_REPOSITORY=my-registry/cluster-controller
```

#### `make deploy-loadtest-only`
Deploy the loadtest environment without building/pushing a new image. Uses existing image from registry.

**Use when:**
- Image already exists in registry
- You want to redeploy with different values
- You want to upgrade Helm configuration without rebuilding

**Example:**
```bash
# Deploy existing image
make deploy-loadtest-only VERSION=v1.2.3

# Update configuration without rebuilding
make deploy-loadtest-only VERSION=v1.2.3 LOADTEST_VALUES_FILE=updated-values.yaml
```

#### `make undeploy-loadtest`
Remove the entire loadtest environment (all Helm releases in castai-agent namespace).

**Example:**
```bash
make undeploy-loadtest
```

### Management Targets

#### `make loadtest-status`
Check the status of the loadtest deployment including Helm releases, pods, and services.

**Example:**
```bash
make loadtest-status
```

**Output:**
```
==> Helm releases:
NAME                    NAMESPACE       STATUS          CHART
castai-loadtest         castai-agent    deployed        castai-loadtest-0.1.0
cluster-controller      castai-agent    deployed        castai-cluster-controller-x.y.z
kwok                    castai-agent    deployed        kwok-x.y.z

==> Pods:
NAME                                           READY   STATUS    RESTARTS
castai-loadtest-agent-xxx                     1/1     Running   0
castai-loadtest-grafana-xxx                   1/1     Running   0
castai-loadtest-prometheus-server-xxx         1/1     Running   0
...

==> Services:
NAME                                  TYPE        PORT(S)
castai-loadtest-agent-service         ClusterIP   8080/TCP
castai-loadtest-grafana               ClusterIP   80/TCP
castai-loadtest-prometheus-server     ClusterIP   9090/TCP
...
```

#### `make loadtest-logs`
Tail logs from the loadtest agent in real-time.

**Example:**
```bash
make loadtest-logs
```

#### `make loadtest-forward-grafana`
Port-forward Grafana to `localhost:3000`.

**Example:**
```bash
make loadtest-forward-grafana
# Open http://localhost:3000 in browser
```

#### `make loadtest-forward-prometheus`
Port-forward Prometheus to `localhost:9090`.

**Example:**
```bash
make loadtest-forward-prometheus
# Open http://localhost:9090 in browser
```

#### `make loadtest-helm-deps`
Build Helm chart dependencies (download and update subchart versions).

**Use when:**
- First time setting up
- After updating Chart.yaml dependencies
- Chart.lock is out of sync

**Example:**
```bash
make loadtest-helm-deps
```

## Complete Workflow Examples

### Example 1: Development Workflow

```bash
# 1. Build and deploy with a feature branch tag
make deploy-loadtest VERSION=feature-auth-fix

# 2. Check status
make loadtest-status

# 3. View logs
make loadtest-logs

# 4. Access Grafana to monitor
make loadtest-forward-grafana
# Open http://localhost:3000

# 5. When done, clean up
make undeploy-loadtest
```

### Example 2: Testing Different Configurations

```bash
# 1. Create test values
cat > test-config.yaml <<EOF
prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
loadtestAgent:
  enabled: true
EOF

# 2. Deploy with custom config
make deploy-loadtest VERSION=v1.0.0 LOADTEST_VALUES_FILE=test-config.yaml

# 3. Update config without rebuilding
cat > test-config-v2.yaml <<EOF
prometheus:
  server:
    resources:
      requests:
        memory: "32Gi"
EOF

make deploy-loadtest-only VERSION=v1.0.0 LOADTEST_VALUES_FILE=test-config-v2.yaml

```

### Example 3: Production-like Load Test

```bash
# 1. Create production-like configurations
cat > cc-prod.yaml <<EOF
resources:
  requests:
    memory: "4Gi"
    cpu: "2000m"
replicaCount: 3
autoscaling:
  enabled: true
EOF

cat > loadtest-prod.yaml <<EOF
loadtestAgent:
  resources:
    requests:
      memory: "2Gi"
prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"
        cpu: "8"
EOF

# 2. Deploy
make deploy-loadtest \
  VERSION=v2.1.0 \
  CC_VALUES_FILE=cc-prod.yaml \
  LOADTEST_VALUES_FILE=loadtest-prod.yaml \
  KWOK_VALUES_FILE=kwok-prod.yaml

# 3. Monitor in Grafana
make loadtest-forward-grafana
```

### Example 4: Multi-Architecture Build

```bash
# Build for amd64 (even on ARM Mac)
make deploy-loadtest VERSION=v1.0.0 ARCH=amd64

# Build for arm64
make deploy-loadtest VERSION=v1.0.0 ARCH=arm64
```

### Example 5: Rapid Iteration

```bash
# Build and deploy
make deploy-loadtest VERSION=dev-$(git rev-parse --short HEAD)

# Make changes to code...

# Rebuild and redeploy
make deploy-loadtest VERSION=dev-$(git rev-parse --short HEAD)

# View logs to check changes
make loadtest-logs
```

## Environment Variables

These can be set in your shell or passed to make:

```bash
# Pass inline
make deploy-loadtest \
  VERSION=v1.2.3 \
  DOCKER_REPOSITORY=my-registry/cluster-controller \
  KWOK_VALUES_FILE=kwok-values.yaml \
  CC_VALUES_FILE=cc-values.yaml \
  LOADTEST_VALUES_FILE=my-values.yaml
```

## Common Issues

### Issue: `VERSION` not set
```
Error: VERSION variable is required
```

**Solution:** Always specify VERSION
```bash
make deploy-loadtest VERSION=v1.0.0
```

### Issue: Helm dependencies not built
```
Error: found in Chart.yaml, but missing in charts/ directory
```

**Solution:** Build dependencies first
```bash
make loadtest-helm-deps
make deploy-loadtest-only VERSION=v1.0.0
```

### Issue: Image not found in registry
```
Error: Failed to pull image
```

**Solution:** Make sure image was pushed
```bash
# Build and push explicitly
make release VERSION=v1.0.0

# Then deploy
make deploy-loadtest-only VERSION=v1.0.0
```

### Issue: Port-forward already in use
```
Error: bind: address already in use
```

**Solution:** Kill existing port-forward
```bash
# Find the process
lsof -ti:3000 | xargs kill -9

# Or use a different port
kubectl port-forward -n castai-agent svc/castai-loadtest-grafana 3001:80
```

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: Deploy Loadtest

on:
  push:
    tags:
      - 'v*'

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Build and Deploy
        env:
          VERSION: ${{ github.ref_name }}
        run: |
          make deploy-loadtest
```

### GitLab CI Example

```yaml
deploy-loadtest:
  stage: deploy
  script:
    - make deploy-loadtest VERSION=$CI_COMMIT_TAG
  only:
    - tags
```

## Tips

1. **Use semantic versioning for VERSION**: `v1.2.3` instead of `latest`
2. **Keep values files in version control**: Track your test configurations
3. **Use `deploy-loadtest-only` for quick iterations**: Saves time when testing configs
4. **Monitor with Grafana**: Always check dashboards after deployment
5. **Clean up when done**: Run `make undeploy-loadtest` to free resources

## See Also

- [README.md](README.md) - General loadtest documentation
- [USAGE.md](USAGE.md) - Detailed deploy-helm.sh usage

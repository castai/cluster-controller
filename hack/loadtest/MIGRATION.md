# Migration Guide: kubectl apply → Helm

This guide explains the migration from direct Kubernetes resource deployment to Helm-based deployment.

## What Changed

### Before (kubectl + kustomize)
- Direct Kubernetes YAML files (`loadtest-components.yaml`)
- Kustomize for ConfigMap generation
- Manual `envsubst` for image templating
- All-in-one monolithic YAML file (590 lines)
- Manual resource management

### After (Helm)
- Proper Helm chart with dependencies
- Official Helm charts for observability components
- Values-based configuration
- Modular and maintainable structure
- Automatic dependency management
- Easy upgrades and rollbacks

## File Mapping

| Old | New | Purpose |
|-----|-----|---------|
| `loadtest-components.yaml` | `chart/templates/*.yaml` | Kubernetes resources (now templated) |
| `kustomization.yaml` | `chart/Chart.yaml` | Configuration and dependencies |
| `grafana/*.yaml` | `chart/values.yaml` | Grafana configuration (now in values) |
| `grafana/*.json` | `chart/dashboards/*.json` | Dashboard files (moved) |
| `deploy.sh` | `deploy-helm.sh` | Deployment script (now uses Helm) |
| N/A | `undeploy-helm.sh` | New: Clean uninstall script |
| N/A | `chart/values.yaml` | New: Centralized configuration |

## Component Changes

### 1. Prometheus
**Before**: Custom deployment with manual configuration
**After**: Uses official `prometheus-community/prometheus` chart

Benefits:
- Maintained by Prometheus community
- Regular updates and security patches
- More configuration options
- Better resource management

### 2. Grafana
**Before**: Custom deployment with ConfigMap injection
**After**: Uses official `grafana/grafana` chart

Benefits:
- Proper dashboard provisioning
- Built-in datasource configuration
- Better plugin management
- Official support

### 3. Loki
**Before**: Simple single-container deployment
**After**: Uses official `grafana/loki` chart

Benefits:
- Production-ready configuration
- Better performance tuning
- Scalability options
- Official support

### 4. Promtail
**Before**: DaemonSet with manual configuration
**After**: Uses official `grafana/promtail` chart

Benefits:
- Proper DaemonSet management
- Better node affinity handling
- Auto-discovery improvements
- Official support

### 5. kube-state-metrics
**Before**: Custom deployment
**After**: Uses official `prometheus-community/kube-state-metrics` chart

Benefits:
- Latest metrics support
- Better RBAC configuration
- Performance optimizations
- Official support

### 6. Load Test Agent
**Before**: Direct deployment in monolithic YAML
**After**: Custom Helm templates in `chart/templates/`

Benefits:
- Proper templating with values
- Easy image updates
- Better resource management
- Clean separation of concerns

## Configuration Migration

### Image Configuration

**Before**:
```bash
export LOAD_TEST_IMAGE_REPOSITORY="..."
export LOAD_TEST_IMAGE_TAG="..."
./deploy.sh
```

**After**:
```bash
# Option 1: Environment variables (still works)
export LOADTEST_IMAGE_REPOSITORY="..."
export LOADTEST_IMAGE_TAG="..."
./deploy-helm.sh

# Option 2: Values file (recommended)
cat > my-values.yaml <<EOF
loadtestAgent:
  image:
    repository: "us-docker.pkg.dev/castai-hub/library/cluster-controller"
    tag: "v1.2.3"
EOF
./deploy-helm.sh my-values.yaml
```

### Resource Limits

**Before**: Hard-coded in YAML
```yaml
resources:
  requests:
    memory: "8Gi"
    cpu: "4"
```

**After**: Configurable via values
```yaml
# chart/values.yaml or custom values file
prometheus:
  server:
    resources:
      requests:
        memory: "16Gi"  # Easy to change
        cpu: "8"
```

### Enabling/Disabling Components

**Before**: Edit YAML file, comment out resources
**After**: Simple boolean flags
```yaml
# Disable Loki if not needed
loki:
  enabled: false

# Disable load test agent
loadtestAgent:
  enabled: false
```

## Deployment Comparison

### Before

```bash
# Step 1: Deploy kwok
helm upgrade --install kwok kwok/kwok ...

# Step 2: Deploy cluster controller (optional)
helm upgrade --install cluster-controller ...

# Step 3: Deploy everything else with kubectl
kubectl kustomize . | envsubst ... | kubectl apply -f -
```

Problems:
- Mixed deployment methods (Helm + kubectl)
- No easy way to manage updates
- Manual dependency handling
- Hard to rollback

### After

```bash
# One command to deploy everything
./deploy-helm.sh

# Or with custom values
./deploy-helm.sh my-values.yaml
```

Benefits:
- Consistent Helm-based deployment
- Automatic dependency management
- Easy upgrades: `helm upgrade castai-loadtest ./chart`
- Easy rollbacks: `helm rollback castai-loadtest`
- Clean uninstall: `./undeploy-helm.sh`

## Upgrade Process

### Updating the Chart

```bash
# 1. Modify values
vim chart/values.yaml

# 2. Upgrade
helm upgrade castai-loadtest ./chart -n castai-agent
```

### Updating Dependencies

```bash
# 1. Update versions in Chart.yaml
vim chart/Chart.yaml

# 2. Update dependencies
cd chart && helm dependency update

# 3. Upgrade
helm upgrade castai-loadtest ./chart -n castai-agent
```

### Updating Images

```bash
# Option 1: CLI override
helm upgrade castai-loadtest ./chart -n castai-agent \
  --set loadtestAgent.image.tag=v1.2.3

# Option 2: Values file
echo "loadtestAgent:
  image:
    tag: v1.2.3" > override.yaml
helm upgrade castai-loadtest ./chart -n castai-agent -f override.yaml

# Option 3: Environment variables with deploy script
export LOADTEST_IMAGE_TAG="v1.2.3"
./deploy-helm.sh
```

## Rollback

### Before
```bash
# Manual: reapply old YAML files
kubectl apply -f old-loadtest-components.yaml
```

### After
```bash
# Automatic: Helm tracks revisions
helm rollback castai-loadtest -n castai-agent

# Or rollback to specific revision
helm rollback castai-loadtest 5 -n castai-agent
```

## Status and Debugging

### Before
```bash
# Check individual resources
kubectl get pods -n castai-agent
kubectl get configmaps -n castai-agent
kubectl get services -n castai-agent
```

### After
```bash
# Helm gives unified view
helm status castai-loadtest -n castai-agent
helm list -n castai-agent

# View all resources managed by Helm
helm get manifest castai-loadtest -n castai-agent

# Plus regular kubectl still works
kubectl get pods -n castai-agent
```

## Benefits Summary

1. **Maintainability**: Modular structure, easy to understand and modify
2. **Reliability**: Uses official, well-tested Helm charts
3. **Configurability**: Centralized values, easy overrides
4. **Upgradability**: Simple upgrade path with rollback support
5. **Consistency**: Single deployment method (Helm)
6. **Dependency Management**: Automatic dependency resolution
7. **Production-Ready**: Based on production-grade charts
8. **Documentation**: Better documentation and examples

## Testing the Migration

1. **Test in a new cluster/namespace**:
   ```bash
   export NAMESPACE="loadtest-test"
   ./deploy-helm.sh
   ```

2. **Verify all components are running**:
   ```bash
   kubectl get pods -n loadtest-test
   helm list -n loadtest-test
   ```

3. **Access Grafana and verify dashboards**:
   ```bash
   kubectl port-forward -n loadtest-test svc/castai-loadtest-grafana 3000:80
   ```

4. **Clean up test**:
   ```bash
   export NAMESPACE="loadtest-test"
   ./undeploy-helm.sh
   kubectl delete namespace loadtest-test
   ```

## Keeping Old Setup (Optional)

The old files are preserved for reference:
- `loadtest-components.yaml` - Original resources
- `kustomization.yaml` - Original kustomize config
- `deploy.sh` - Original deploy script

You can keep using the old setup if needed, but the new Helm-based approach is recommended.

## Need Help?

- Chart documentation: See `README.md`
- Helm docs: https://helm.sh/docs/
- Report issues: Create an issue in the repository

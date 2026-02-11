#!/usr/bin/env bash

set -e

# Optional values files can be passed as arguments or environment variables
# Usage:
#   ./deploy-helm.sh                                    # Use defaults
#   ./deploy-helm.sh loadtest-values.yaml               # Single values file for loadtest only
#   CC_VALUES_FILE=cc.yaml ./deploy-helm.sh             # Cluster controller values via env var
#   CC_VALUES_FILE=cc.yaml ./deploy-helm.sh lt.yaml     # Separate values for both
LOADTEST_VALUES_FILE="${1:-}"

# Configuration
LOADTEST_CHART_PATH="${LOADTEST_CHART_PATH:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chart}"
LOADTEST_IMAGE_REPOSITORY="${LOADTEST_IMAGE_REPOSITORY:-us-docker.pkg.dev/castai-hub/library/cluster-controller}"
LOADTEST_IMAGE_TAG="${LOADTEST_IMAGE_TAG:-latest}"
CC_IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-$LOADTEST_IMAGE_REPOSITORY}"
CC_IMAGE_TAG="${IMAGE_TAG:-$LOADTEST_IMAGE_TAG}"
CC_VALUES_FILE="${CC_VALUES_FILE:-}"  # Cluster controller values file
DEPLOY_CLUSTER_CONTROLLER="${DEPLOY_CLUSTER_CONTROLLER:-true}"
KWOK_REPLICAS="${KWOK_REPLICAS:-10}"
NAMESPACE="${NAMESPACE:-castai-agent}"
RELEASE_NAME="${RELEASE_NAME:-castai-loadtest}"

echo "==> Adding required Helm repositories"
helm repo add kwok https://kwok.sigs.k8s.io/charts/ || true
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
helm repo add grafana https://grafana.github.io/helm-charts || true
helm repo add castai-helm https://castai.github.io/helm-charts || true
helm repo update

echo "==> Deploying kwok (fake nodes)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KWOK_VALUES_PATH="$SCRIPT_DIR/kwok-values.yaml"
KWOK_FILTER="$SCRIPT_DIR/kwok-filter.sh"

# Deploy kwok with post-renderer to filter out FlowSchema and PriorityClass
echo "  Filtering out FlowSchema and PriorityClass resources..."
helm upgrade --namespace "$NAMESPACE" --create-namespace --install kwok kwok/kwok \
  --set "replicas=$KWOK_REPLICAS" \
  --values "$KWOK_VALUES_PATH" \
  --post-renderer "$KWOK_FILTER"

helm upgrade --namespace "$NAMESPACE" --create-namespace --install kwok-stages kwok/stage-fast

helm upgrade --namespace "$NAMESPACE" --create-namespace --install kwok-metrics kwok/metrics-usage

if [ "$DEPLOY_CLUSTER_CONTROLLER" = "true" ]; then
  echo "==> Deploying cluster controller"
  HELM_ARGS=(
    --namespace "$NAMESPACE"
    --create-namespace
    --install cluster-controller
    castai-helm/castai-cluster-controller
    --set castai.apiKey="dummy"
    --set castai.apiURL="http://castai-loadtest-agent-service.$NAMESPACE.svc.cluster.local.:8080"
    --set castai.clusterID="00000000-0000-0000-0000-000000000000"
    --set image.repository="$CC_IMAGE_REPOSITORY"
    --set image.tag="$CC_IMAGE_TAG"
    --set image.pullPolicy="Always"
    --set autoscaling.enabled="true"
  )

  if [ -n "$CC_VALUES_FILE" ]; then
    echo "Using cluster controller values file: $CC_VALUES_FILE"
    HELM_ARGS+=(--values "$CC_VALUES_FILE")
  fi

  helm upgrade "${HELM_ARGS[@]}"
fi

echo "==> Building Helm dependencies for loadtest chart"
helm dependency build "$LOADTEST_CHART_PATH"

echo "==> Deploying load testing observability stack"
LOADTEST_HELM_ARGS=(
  --namespace "$NAMESPACE"
  --create-namespace
  --install "$RELEASE_NAME"
  "$LOADTEST_CHART_PATH"
  --set loadtestAgent.image.repository="$LOADTEST_IMAGE_REPOSITORY"
  --set loadtestAgent.image.tag="$LOADTEST_IMAGE_TAG"
)

if [ -n "$LOADTEST_VALUES_FILE" ]; then
  echo "Using loadtest values file: $LOADTEST_VALUES_FILE"
  LOADTEST_HELM_ARGS+=(--values "$LOADTEST_VALUES_FILE")
fi

helm upgrade "${LOADTEST_HELM_ARGS[@]}"

echo ""
echo "==> Deployment complete!"
echo ""
echo "To access Grafana:"
echo "  kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME-grafana 3000:80"
echo "  Then open http://localhost:3000"
echo ""
echo "To access Prometheus:"
echo "  kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME-prometheus-server 9090:9090"
echo "  Then open http://localhost:9090"
echo ""

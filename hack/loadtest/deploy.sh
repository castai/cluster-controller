#!/usr/bin/env bash

CC_IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-us-docker.pkg.dev/castai-hub/library/cluster-controller}"
CC_IMAGE_TAG="${IMAGE_TAG:-latest}"
LOAD_TEST_IMAGE_REPOSITORY="${LOAD_TEST_IMAGE_REPOSITORY:-$CC_IMAGE_REPOSITORY}"
LOAD_TEST_IMAGE_TAG="${LOAD_TEST_IMAGE_TAG:-$CC_IMAGE_TAG}"
DEPLOY_CLUSTER_CONTROLLER="${DEPLOY_CLUSTER_CONTROLLER:-true}"

# Determine the directory where the script resides.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Deploying kwok"
helm repo add kwok https://kwok.sigs.k8s.io/charts/
helm repo update kwok

helm upgrade --namespace castai-agent --create-namespace  --install kwok kwok/kwok
helm upgrade --namespace castai-agent --create-namespace  --install kwok-stages kwok/stage-fast
helm upgrade --namespace castai-agent --create-namespace  --install kwok-metrics kwok/metrics-usage

if [ "$DEPLOY_CLUSTER_CONTROLLER" = "true" ]; then
  echo "Deploying cluster controller"
  helm upgrade --namespace castai-agent --create-namespace --install  cluster-controller castai-helm/castai-cluster-controller \
    --set castai.apiKey="dummy" \
    --set castai.apiURL="http://castai-loadtest-agent-service.castai-agent.svc.cluster.local.:8080" \
    --set castai.clusterID="00000000-0000-0000-0000-000000000000" \
    --set image.repository="$CC_IMAGE_REPOSITORY" \
    --set image.tag="$CC_IMAGE_TAG" \
    --set image.pullPolicy="Always" \
    --set autoscaling.enabled="true"
fi

echo "Deploying load testing components"
kubectl kustomize "$SCRIPT_DIR" | \
  LOADTEST_REPOSITORY="$LOAD_TEST_IMAGE_REPOSITORY" LOADTEST_TAG="$LOAD_TEST_IMAGE_TAG" envsubst \$LOADTEST_REPOSITORY,\$LOADTEST_TAG | \
  kubectl apply -f -

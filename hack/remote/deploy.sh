#!/bin/sh

set -e

# Go to git repo root.
cd "$(git rev-parse --show-toplevel)"

if [ -z "$API_KEY" ]; then
  echo "API_KEY environment variable is not defined"
  exit 1
fi

if [ -z "$API_URL" ]; then
  echo "API_URL environment variable is not defined"
  exit 1
fi

if [ -z "$CLUSTER_ID" ]; then
  echo "CLUSTER_ID environment variable is not defined"
  exit 1
fi

# Build bo binary and push docker image.
IMAGE_TAG=v0.0.1
GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=${IMAGE_TAG}" -o bin/castai-cluster-controller .
DOCKER_IMAGE_REPO=europe-west3-docker.pkg.dev/ci-master-mo3d/tilt/$USER/castai-cluster-controller
docker build -t "$DOCKER_IMAGE_REPO:$IMAGE_TAG" .
docker push "$DOCKER_IMAGE_REPO:$IMAGE_TAG"

# Install local chart and binary.
LOCAL_CHART_DIR=../gh-helm-charts/charts/castai-cluster-controller
helm upgrade -i cluster-controller $LOCAL_CHART_DIR \
  -f ./hack/remote/values.yaml \
  --set image.repository="${DOCKER_IMAGE_REPO}" \
  --set image.tag="${IMAGE_TAG}" \
  --set aks.enabled=false \
  --set serviceAccount.create="true" \
  --set castai.apiKey="${API_KEY}" \
  --set castai.apiURL="${API_URL}" \
  --set castai.clusterID="${CLUSTER_ID}" \
  --history-max=3 \
  -n castai-agent

kubectl rollout restart deployment castai-cluster-controller -n castai-agent

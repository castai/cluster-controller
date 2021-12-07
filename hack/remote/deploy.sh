#!/bin/sh

set -e

# Go to git repo root.
cd "$(git rev-parse --show-toplevel)"

# Build bo binary and push docker image.
GOOS=linux go build -o bin/castai-cluster-controller .
DOCKER_IMAGE_REPO=europe-west3-docker.pkg.dev/ci-master-mo3d/tilt/$USER/castai-cluster-controller
IMAGE_TAG=latest
docker build -t $DOCKER_IMAGE_REPO:$IMAGE_TAG .
docker push $DOCKER_IMAGE_REPO:$IMAGE_TAG

# Install local chart and binary.
LOCAL_CHART_DIR=../gh-helm-charts/charts/castai-cluster-controller
helm upgrade cluster-controller $LOCAL_CHART_DIR \
  -f ./hack/remote/values.yaml \
  --set image.repository="$DOCKER_IMAGE_REPO" \
  --reuse-values \
  --history-max=3 \
  -n castai-agent

kubectl rollout restart deployment castai-cluster-controller -n castai-agent

#!/bin/sh

set -e

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

# go to git repo root
cd "$(git rev-parse --show-toplevel)"

GOOS=linux go build -o bin/castai-cluster-controller .

DOCKER_IMAGE_REPO=europe-west3-docker.pkg.dev/ci-master-mo3d/tilt/$USER/castai-cluster-controller
IMAGE_TAG=latest
docker build -t $DOCKER_IMAGE_REPO:$IMAGE_TAG .
docker push $DOCKER_IMAGE_REPO:$IMAGE_TAG

helm template cluster-controller castai-helm/castai-cluster-controller \
  -f ./hack/remote/values.yaml \
  --set image.repository="$DOCKER_IMAGE_REPO" \
  --set apiKey="$API_KEY" \
  --set apiURL="$API_URL" \
  --set clusterID="$CLUSTER_ID" | kubectl apply -f - -n castai-agent

kubectl rollout restart deployment castai-cluster-controller -n castai-agent

#!/bin/sh
#
# Build castai-tunnel Docker image and push to registry.
#
# Usage:
#   IMAGE_TAG=v1 ./hack/remote/build-tunnel.sh
#
# Optional overrides:
#   TUNNEL_DOCKER_REPOSITORY=docker.io/myuser/castai-tunnel IMAGE_TAG=v2 ./hack/remote/build-tunnel.sh
#   ARCH=arm64 IMAGE_TAG=v3 ./hack/remote/build-tunnel.sh
#

set -e

cd "$(git rev-parse --show-toplevel)"

DOCKER_IMAGE_REPO="${TUNNEL_DOCKER_REPOSITORY:-us-docker.pkg.dev/castai-hub/library/cluster-controller-tunnel}"
ARCH="${ARCH:-amd64}"

if [ -z "$IMAGE_TAG" ]; then
  echo "IMAGE_TAG environment variable is required"
  echo "Usage: IMAGE_TAG=v1 ./hack/remote/build-tunnel.sh"
  exit 1
fi

echo "==> Building castai-tunnel binary (linux/$ARCH)..."
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -ldflags "-s -w" -o "bin/castai-tunnel-$ARCH" ./cmd/tunnel

echo "==> Building Docker image ${DOCKER_IMAGE_REPO}:${IMAGE_TAG}..."
docker build --platform="linux/$ARCH" --build-arg "TARGETARCH=$ARCH" -f Dockerfile.tunnel -t "${DOCKER_IMAGE_REPO}:${IMAGE_TAG}" .

echo "==> Pushing ${DOCKER_IMAGE_REPO}:${IMAGE_TAG}..."
docker push "${DOCKER_IMAGE_REPO}:${IMAGE_TAG}"

echo "==> Done: ${DOCKER_IMAGE_REPO}:${IMAGE_TAG}"

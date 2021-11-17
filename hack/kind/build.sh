#!/bin/sh

set -e

# go to git repo root
cd "$(git rev-parse --show-toplevel)"

GOOS=linux go build -o bin/castai-cluster-controller .
docker build -t localhost:5000/castai-cluster-controller:latest .
docker push localhost:5000/castai-cluster-controller:latest

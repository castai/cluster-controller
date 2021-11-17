#!/bin/sh

set -e

if [ -z "$DOCKER_SECRET_TMPL_PATH" ]; then
  echo "DOCKER_SECRET_TMPL_PATH environment variable is not defined"
  exit 1
fi

helm repo add castai-helm https://castai.github.io/helm-charts
helm repo update

$DOCKER_SECRET_TMPL_PATH castai-agent | kubectl apply -f - -n castai-agent
kubectl patch serviceaccount castai-cluster-controller -p '{"imagePullSecrets": [{"name": "artifact-registry"}]}'
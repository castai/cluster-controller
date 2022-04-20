#!/bin/sh

set -e

if [ -z "$DOCKER_SECRET_TMPL_PATH" ]; then
  echo "DOCKER_SECRET_TMPL_PATH environment variable is not defined"
  exit 1
fi

$DOCKER_SECRET_TMPL_PATH castai-agent | kubectl apply -f - -n castai-agent


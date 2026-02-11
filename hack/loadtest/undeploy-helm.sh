#!/usr/bin/env bash

set -e

# Configuration
NAMESPACE="${NAMESPACE:-castai-agent}"
RELEASE_NAME="${RELEASE_NAME:-castai-loadtest}"

echo "==> Uninstalling load testing observability stack"
helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" || true

echo "==> Uninstalling cluster controller"
helm uninstall cluster-controller -n "$NAMESPACE" || true

echo "==> Uninstalling kwok components"
helm uninstall kwok-metrics -n "$NAMESPACE" || true
helm uninstall kwok-stages -n "$NAMESPACE" || true
helm uninstall kwok -n "$NAMESPACE" || true

echo ""
echo "==> Uninstall complete!"
echo ""
echo "To delete the namespace entirely:"
echo "  kubectl delete namespace $NAMESPACE"
echo ""

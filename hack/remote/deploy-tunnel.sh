#!/bin/sh
#
# Deploy castai-tunnel to a Kubernetes cluster.
# Reads API_URL, API_KEY, CLUSTER_ID from the existing castai-cluster-controller
# ConfigMap and Secret (created by the Helm chart).
#
# Usage:
#   IMAGE_TAG=v1 ./hack/remote/deploy-tunnel.sh
#
# Optional overrides:
#   NAMESPACE=my-ns IMAGE_TAG=v2 ./hack/remote/deploy-tunnel.sh
#   TUNNEL_DOCKER_REPOSITORY=docker.io/myuser/castai-tunnel IMAGE_TAG=v3 ./hack/remote/deploy-tunnel.sh
#

set -e

DOCKER_IMAGE_REPO="${TUNNEL_DOCKER_REPOSITORY:-docker.io/krystiancast/castai-tunnel}"
NAMESPACE="${NAMESPACE:-castai-agent}"

if [ -z "$IMAGE_TAG" ]; then
  echo "IMAGE_TAG environment variable is required"
  echo "Usage: IMAGE_TAG=v1 ./hack/remote/deploy-tunnel.sh"
  exit 1
fi

echo "==> Deploying ${DOCKER_IMAGE_REPO}:${IMAGE_TAG} to namespace ${NAMESPACE}..."

kubectl apply -n "$NAMESPACE" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: castai-tunnel
  labels:
    app: castai-tunnel
spec:
  replicas: 1
  selector:
    matchLabels:
      app: castai-tunnel
  template:
    metadata:
      labels:
        app: castai-tunnel
    spec:
      serviceAccountName: castai-cluster-controller
      containers:
        - name: castai-tunnel
          image: ${DOCKER_IMAGE_REPO}:${IMAGE_TAG}
          imagePullPolicy: Always
          envFrom:
            - configMapRef:
                name: castai-cluster-controller
            - secretRef:
                name: castai-cluster-controller
          ports:
            - name: health
              containerPort: 8091
              protocol: TCP
          livenessProbe:
            httpGet:
              path: /healthz
              port: health
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: health
            initialDelaySeconds: 3
            periodSeconds: 5
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              memory: 128Mi
EOF

kubectl rollout restart deployment castai-tunnel -n "$NAMESPACE"
kubectl rollout status deployment castai-tunnel -n "$NAMESPACE" --timeout=60s

echo "==> Done: castai-tunnel deployed with ${DOCKER_IMAGE_REPO}:${IMAGE_TAG}"

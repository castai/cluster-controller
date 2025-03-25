helm repo add kwok https://kwok.sigs.k8s.io/charts/
helm repo update kwok

helm upgrade --namespace castai-agent --create-namespace  --install kwok kwok/kwok
helm upgrade --namespace castai-agent --create-namespace  --install kwok-stages kwok/stage-fast
helm upgrade --namespace castai-agent --create-namespace  --install kwok-metrics kwok/metrics-usage

# TODO: Tune kwok to be faster?


helm upgrade --namespace castai-agent --create-namespace --install  cluster-controller castai-helm/castai-cluster-controller \
  --set castai.apiKey="dummy" \
  --set castai.apiURL="http://castai-loadtest-agent-service.castai-agent.svc.cluster.local.:8080" \
  --set castai.clusterID="00000000-0000-0000-0000-000000000000" \
  --set image.repository=docker.io/lachezarcast/cluster-controller \
  --set image.tag="loadtest" \
  --set image.pullPolicy="Always" \
  --set autoscaling.enabled="true"



# Config for load test server
# Config for prometheus
# Config for grafana?

# Kwok !
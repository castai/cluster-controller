# Load testing Cluster controller

Load test requires 3 components:
- Test server that simulates cluster-hub and the scenarios. 
- Kwok controller to simulate nodes/pods
- Cluster controller itself.

Optionally, observability stack helps identify problems with the deployment.

## Local run
This runs all 3 components as local processes against a cluster. 
Useful for debugging. https://github.com/arl/statsviz can be used for local observability.

Start kwok:
```
 kwok --kubeconfig=~/.kube/config \
    --manage-all-nodes=false \
    --manage-nodes-with-annotation-selector=kwok.x-k8s.io/node=fake \
    --node-lease-duration-seconds=40 \
    --cidr=10.0.0.1/24 \
    --node-ip=10.0.0.1
```

Run the test server on port 8080 against your current kubeconfig context:
```
KUBECONFIG=~/.kube/config PORT=8080 go run . test-server
```

After starting, start cluster controller with some dummy values and point it to the test server:
```
API_KEY=dummy API_URL=http://localhost:8080 CLUSTER_ID=D30A163C-C5DF-4CC8-985C-D1449398295E KUBECONFIG=~/.kube/config LOG_LEVEL=4 LEADER_ELECTION_NAMESPACE=default METRICS_ENABLED=true go run .                     
```

## Deployment in cluster
Running the command below will build the local cluster controller, push it to a repository and deploy all 3 required components + observability stack into the current cluster.
Both the cluster controller and the test server will use the same image but will run in different modes. 

`make deploy-loadtest DOCKER_REPOSITORY=<repository_for_iamge> VERSION=<tag> ARCH=amd64`

If you wish to skip deploying cluster controller, prefix make with `DEPLOY_CLUSTER_CONTROLLER=false`. Be sure to update the existing cluster controller to use the deployed test server's URL.

If you wish to use different repository for cluster controller and for test server, pass `LOAD_TEST_IMAGE_REPOSITORY` and `LOAD_TEST_IMAGE_TAG` env vars to the command.

The deploy command also includes prometheus and grafana. 
Use `kubectl port-forward -n castai-agent svc/observability-service 3000:3000` to reach the grafana instance. There is already a preconfigured dashboard available on the instance.
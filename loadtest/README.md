# Load testing Cluster controller

Load test requires 3 components:
- Test server that simulates cluster-hub and the scenarios. 
- Kwok controller to simulate nodes/pods
- Cluster controller itself.

Right now this is extremely basic and you have to run those manually locally.

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

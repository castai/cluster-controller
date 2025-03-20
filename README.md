# CAST AI cluster controller

The official CAST AI kubernetes cluster controller written in Go

## Installation

Check our official helm charts repo https://github.com/castai/castai-helm-charts

## Testing

### Pull requests

Each pull request builds and publishes docker image for easier code review and testing. Check relevant GitHub actions.

### On existing cluster enrolled to CAST AI

Deploy cluster-controller to already connected remote cluster.

*NOTE*: Make sure your kubectl context is pointing to your remote cluster.

Have a configured `gcloud`. Make sure to docker login with
```shell
gcloud auth configure-docker gcr.io
```

Clone https://github.com/castai/castai-helm-charts adjacent to repo root folder. It will be used by our scripts
```shell
cd <cluster-controller-parent-directory>
git clone https://github.com/castai/castai-helm-charts gh-helm-charts
```

Deploy.

```shell
API_KEY=your-api-key \
API_URL=your-api-url \
CLUSTER_ID=your-cluster-id \
./hack/remote/deploy.sh
```

### Local

```shell
API_KEY=your-api-key \
API_URL=your-api-url \
CLUSTER_ID=your-cluster-id \
KUBECONFIG=path-to-kubeconfig \
go run .
```

### Kind

The cluster-controller can be tested locally with a full e2e flow using `kind`: [Kubernetes in Docker](https://kind.sigs.k8s.io/).

Setup a `kind` cluster with a local docker registry by running the `./hack/kind/run.sh` script.

Option 1. Deploy controller in Kind cluster.
* Build your local code and push it to the local registry with `./hack/kind/build.sh`.
* Deploy the chart to the `kind` cluster with
  ```shell
  helm repo add castai-helm https://castai.github.io/helm-charts
  helm repo update
  helm template cluster-controller castai-helm/castai-cluster-controller \
    -f hack/kind/values.yaml \
    --set apiKey="your-api-key" \
    --set apiURL="your-api-url" \
    --set clusterID="your-cluster-id" | kubectl apply -f - -n castai-agent
  ```

### Load tests
See [docs](loadtest/README.md)

## Community

- [Twitter](https://twitter.com/cast_ai)
- [Discord](https://discord.gg/4sFCFVJ)

## Contributing

Please see the [contribution guidelines](.github/CONTRIBUTING.md).

## License

Code is licensed under the [Apache License 2.0](LICENSE). See [NOTICE.md](NOTICE.md) for complete details, including software and third-party licenses and permissions.

export API_TAGS ?= ExternalClusterAPI,AuthTokenAPI,OperationsAPI,AutoscalerAPI
export SWAGGER_LOCATION ?= https://api.cast.ai/v1/spec/openapi.json

GO_INSTALL = ./hack/go-install.sh

TOOLS_DIR=bin
ROOT_DIR=$(abspath .)
TOOLS_GOBIN_DIR := $(abspath $(TOOLS_DIR))

GOLANGCI_LINT_VER := v2.7.2
GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT := $(TOOLS_GOBIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER)

DOCKER_REPOSITORY ?= us-docker.pkg.dev/castai-hub/library/cluster-controller

ARCH ?= $(shell uname -m)
ifeq ($(ARCH),x86_64)
	ARCH=amd64
endif


$(GOLANGCI_LINT):
	GOBIN=$(TOOLS_GOBIN_DIR) $(GO_INSTALL) github.com/golangci/golangci-lint/v2/cmd/golangci-lint $(GOLANGCI_LINT_BIN) $(GOLANGCI_LINT_VER)

## build: Build the binary for the specified architecture and create a Docker image. Usually this means ARCH=amd64 should be set if running on an ARM machine. Use `go build .` for simple local build.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) GOEXPERIMENT=synctest go build -ldflags "-s -w" -o bin/castai-cluster-controller-$(ARCH) .
	docker build --platform=linux/$(ARCH) --build-arg TARGETARCH=$(ARCH) -t $(DOCKER_REPOSITORY):$(VERSION) .

push:
	docker push $(DOCKER_REPOSITORY):$(VERSION)

release: build push

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --timeout 20m ./...
.PHONY: lint

fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --fix ./...
.PHONY: fix

test:
	GOEXPERIMENT=synctest go test ./... -short -race -parallel=20
.PHONY: test

generate-e2e-client:
	go generate ./e2e/client
.PHONY: generate-e2e-client

## deploy-loadtest: Build, push, and deploy the loadtest environment with cluster-controller
deploy-loadtest: release
	IMAGE_REPOSITORY=$(DOCKER_REPOSITORY) \
	IMAGE_TAG=$(VERSION) \
	LOADTEST_IMAGE_REPOSITORY=$(DOCKER_REPOSITORY) \
	LOADTEST_IMAGE_TAG=$(VERSION) \
	./hack/loadtest/deploy-helm.sh \
	$(if $(KWOK_VALUES_FILE),--kwok-values $(KWOK_VALUES_FILE)) \
	$(if $(CC_VALUES_FILE),--cc-values $(CC_VALUES_FILE)) \
	$(if $(LOADTEST_VALUES_FILE),--loadtest-values $(LOADTEST_VALUES_FILE))
.PHONY: deploy-loadtest

## deploy-loadtest-only: Deploy loadtest environment without building (uses existing image)
deploy-loadtest-only:
	IMAGE_REPOSITORY=$(DOCKER_REPOSITORY) \
	IMAGE_TAG=$(VERSION) \
	LOADTEST_IMAGE_REPOSITORY=$(DOCKER_REPOSITORY) \
	LOADTEST_IMAGE_TAG=$(VERSION) \
	./hack/loadtest/deploy-helm.sh \
	$(if $(KWOK_VALUES_FILE),--kwok-values $(KWOK_VALUES_FILE)) \
	$(if $(CC_VALUES_FILE),--cc-values $(CC_VALUES_FILE)) \
	$(if $(LOADTEST_VALUES_FILE),--loadtest-values $(LOADTEST_VALUES_FILE))
.PHONY: deploy-loadtest-only

## undeploy-loadtest: Remove the loadtest environment
undeploy-loadtest:
	./hack/loadtest/undeploy-helm.sh
.PHONY: undeploy-loadtest

## loadtest-status: Check the status of loadtest deployment
loadtest-status:
	@echo "==> Helm releases:"
	@helm list -n castai-agent
	@echo ""
	@echo "==> Pods:"
	@kubectl get pods -n castai-agent
	@echo ""
	@echo "==> Services:"
	@kubectl get svc -n castai-agent | grep -E '(NAME|loadtest|prometheus|grafana|loki)'
.PHONY: loadtest-status

## loadtest-logs: Tail logs from loadtest agent
loadtest-logs:
	kubectl logs -n castai-agent -l app=castai-loadtest-agent -f
.PHONY: loadtest-logs

## loadtest-forward-grafana: Port-forward Grafana to localhost:3000
loadtest-forward-grafana:
	@echo "Grafana will be available at http://localhost:3000"
	kubectl port-forward -n castai-agent svc/castai-loadtest-grafana 3000:80
.PHONY: loadtest-forward-grafana

## loadtest-forward-prometheus: Port-forward Prometheus to localhost:9090
loadtest-forward-prometheus:
	@echo "Prometheus will be available at http://localhost:9090"
	kubectl port-forward -n castai-agent svc/castai-loadtest-prometheus-server 9090:9090
.PHONY: loadtest-forward-prometheus

## loadtest-helm-deps: Build Helm chart dependencies
loadtest-helm-deps:
	helm dependency build ./hack/loadtest/chart
.PHONY: loadtest-helm-deps

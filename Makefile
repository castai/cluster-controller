export API_TAGS ?= ExternalClusterAPI,AuthTokenAPI,OperationsAPI,AutoscalerAPI
export SWAGGER_LOCATION ?= https://api.cast.ai/v1/spec/openapi.json

GO_INSTALL = ./hack/go-install.sh

TOOLS_DIR=bin
ROOT_DIR=$(abspath .)
TOOLS_GOBIN_DIR := $(abspath $(TOOLS_DIR))

GOLANGCI_LINT_VER := v1.62.2
GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT := $(TOOLS_GOBIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER)

$(GOLANGCI_LINT):
	GOBIN=$(TOOLS_GOBIN_DIR) $(GO_INSTALL) github.com/golangci/golangci-lint/cmd/golangci-lint $(GOLANGCI_LINT_BIN) $(GOLANGCI_LINT_VER)

build:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o bin/castai-cluster-controller-amd64 .
	docker build -t us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION) .

push:
	docker push us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION)

release: build push

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --timeout 20m ./...
.PHONY: lint

fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --fix ./...
.PHONY: fix

test:
	go test ./... -race
.PHONY: test

generate-e2e-client:
	go generate ./e2e/client
.PHONY: generate-e2e-client

export API_TAGS ?= ExternalClusterAPI,AuthTokenAPI,OperationsAPI,AutoscalerAPI
export SWAGGER_LOCATION ?= https://api.cast.ai/v1/spec/openapi.json

GO_INSTALL = ./hack/go-install.sh

TOOLS_DIR=bin
ROOT_DIR=$(abspath .)
TOOLS_GOBIN_DIR := $(abspath $(TOOLS_DIR))

GOLANGCI_LINT_VER := v1.64.8
GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT := $(TOOLS_GOBIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER)

DOCKER_REPOSITORY ?= us-docker.pkg.dev/castai-hub/library/cluster-controller

ARCH ?= $(shell uname -m)
ifeq ($(ARCH),x86_64)
	ARCH=amd64
endif


$(GOLANGCI_LINT):
	GOBIN=$(TOOLS_GOBIN_DIR) $(GO_INSTALL) github.com/golangci/golangci-lint/cmd/golangci-lint $(GOLANGCI_LINT_BIN) $(GOLANGCI_LINT_VER)

## build: Build the binary for the specified architecture and create a Docker image. Usually this means ARCH=amd64 should be set if running on an ARM machine. Use `go build .` for simple local build.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -ldflags "-s -w" -o bin/castai-cluster-controller-$(ARCH) .
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
	go test ./... -race -parallel=20
.PHONY: test

generate-e2e-client:
	go generate ./e2e/client
.PHONY: generate-e2e-client

generate-api-client:
	go generate internal/castai/api/generate.go
.PHONY: generate-api-client

deploy-loadtest: release
	IMAGE_REPOSITORY=$(DOCKER_REPOSITORY) IMAGE_TAG=$(VERSION) ./hack/loadtest/deploy.sh

export API_TAGS ?= ExternalClusterAPI,AuthTokenAPI,OperationsAPI,AutoscalerAPI
export SWAGGER_LOCATION ?= https://api.cast.ai/v1/spec/openapi.json

build:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o bin/castai-cluster-controller-amd64 .
	docker build -t us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION) .

push:
	docker push us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION)

release: build push

lint:
	golangci-lint run ./...
.PHONY: lint

fix:
	golangci-lint run --fix ./...
.PHONY: fix

test:
	go test ./... -race
.PHONY: test

generate-e2e-client:
	go generate ./e2e/client
.PHONY: generate-e2e-client

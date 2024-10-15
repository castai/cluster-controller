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
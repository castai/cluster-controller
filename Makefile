build:
	GOOS=linux go build -ldflags "-s -w" -o bin/castai-cluster-controller .
	docker build -t us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION) .

push:
	docker push us-docker.pkg.dev/castai-hub/library/cluster-controller:$(VERSION)

release: build push

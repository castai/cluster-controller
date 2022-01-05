build:
	GOOS=linux go build -ldflags "-s -w" -o bin/castai-cluster-controller .
	docker build -t castai/cluster-controller:$(VERSION) .

push:
	docker push castai/cluster-controller:$(VERSION)

release: build push

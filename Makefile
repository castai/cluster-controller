build:
	GOOS=linux go build -o bin/castai-cluster-controller .
	docker build -t castai/castai-cluster-controller:$(VERSION) .

push:
	docker push castai/castai-cluster-controller:$(VERSION)

release: build push


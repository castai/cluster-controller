FROM alpine:3.13
COPY bin/castai-cluster-controller /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

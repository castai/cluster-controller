FROM gcr.io/distroless/static-debian11
ARG TARGETARCH
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller", "-alsologtostderr=true", "-logtostderr=false"]

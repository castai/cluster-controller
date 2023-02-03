FROM gcr.io/distroless/static:nonroot
ARG TARGETARCH amd64
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

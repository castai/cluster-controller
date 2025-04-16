FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

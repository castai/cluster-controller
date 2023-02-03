ARG TARGETARCH amd64

FROM gcr.io/distroless/static:nonroot-$TARGETARCH
RUN apk add --no-cache openssl
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

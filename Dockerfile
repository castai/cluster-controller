FROM gcr.io/distroless/static:nonroot
RUN apk add --no-cache openssl
ARG TARGETARCH amd64
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

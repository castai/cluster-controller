FROM gcr.io/distroless/static-debian11
RUN apk add --no-cache openssl
ARG TARGETARCH amd64
COPY bin/castai-cluster-controller-$TARGETARCH  /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

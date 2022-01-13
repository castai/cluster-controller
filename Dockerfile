FROM alpine:3.13
RUN apk add --no-cache openssl
COPY bin/castai-cluster-controller /usr/local/bin/castai-cluster-controller
CMD ["castai-cluster-controller"]

FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2
RUN sed -i 's/dl-cdn\.alpinelinux\.org/dl-2.alpinelinux.org/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates iproute2-minimal nftables wireguard-tools-wg
COPY --chmod=0555 node /node
COPY --chmod=0555 node-xray /node-xray
COPY --chmod=0444 LICENSE /LICENSE
USER 65532:65532
ENTRYPOINT ["/node", "-config", "/config.yml"]

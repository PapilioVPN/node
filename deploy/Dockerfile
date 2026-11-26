FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2
RUN for mirror in dl-2.alpinelinux.org mirrors.edge.kernel.org dl-cdn.alpinelinux.org; do \
      sed "s|dl-cdn.alpinelinux.org/alpine|$mirror/alpine|g" /etc/apk/repositories > /tmp/node-repositories; \
      if timeout 60 apk --repositories-file /tmp/node-repositories add --no-cache ca-certificates iproute2-minimal nftables openvpn wireguard-tools-wg; then \
        rm /tmp/node-repositories; exit 0; \
      fi; \
    done; exit 1
COPY --chmod=0555 node /node
COPY --chmod=0555 node-xray /node-xray
COPY --chmod=0444 LICENSE /LICENSE
USER 65532:65532
ENTRYPOINT ["/node", "-config", "/config.yml"]

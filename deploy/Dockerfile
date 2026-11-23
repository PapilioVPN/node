FROM scratch
COPY --chmod=0555 node /node
COPY --chmod=0555 node-xray /node-xray
USER 65532:65532
ENTRYPOINT ["/node", "-config", "/config.yml"]

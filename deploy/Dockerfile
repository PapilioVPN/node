FROM scratch
COPY --chmod=0555 node /node
USER 65532:65532
ENTRYPOINT ["/node", "-config", "/config.yml"]

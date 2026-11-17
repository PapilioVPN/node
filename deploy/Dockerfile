FROM scratch
ARG REVISION
LABEL org.opencontainers.image.source="https://github.com/PapilioVPN/node" \
      org.opencontainers.image.revision="${REVISION}"
COPY --chmod=0555 node /node
USER 65532:65532
ENTRYPOINT ["/node"]
CMD ["-config", "/etc/node/config.yml"]
STOPSIGNAL SIGTERM

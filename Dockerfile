# Native parser/OCR resources from the pinned RC2 image, not downloaded at startup.
FROM infiniflow/ragflow@sha256:2fc19b4c8bbb589e6ea2debd641e1e83a92ad5f9b398c121d46c9f56f296ba6e
COPY VERSION /ragflow/VERSION
COPY bin/ragflow_server /ragflow/bin/ragflow_server
COPY bin/ragflow-cli /ragflow/bin/ragflow-cli
COPY bin/ragflow_pg_migrate /ragflow/bin/ragflow_pg_migrate
# Invoke an explicit mode. Do not inherit the upstream MySQL launch/migration script.
ENTRYPOINT ["/ragflow/bin/ragflow_server"]
CMD ["--help"]
LABEL org.opencontainers.image.version="1.0.0-rc2-vsm-pg3"

# The image ships the cgo build (ODBC and DuckDB). DB_IMAGE provides unixODBC
# and the ODBC drivers, registered in /etc/odbcinst.ini.
ARG DB_IMAGE=ghcr.io/rytsh/dock/db/multi:debian-13.6-slim

FROM ${DB_IMAGE}

RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -M -u 10001 dataport \
    && mkdir -p /data /files && chown dataport /data

# GoReleaser puts the binary under linux/<arch>/ in the context.
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/dataport-cgo /dataport

# The SQLite store and the generated secret key live in /data; mount a volume.
# File databases (SQLite, DuckDB) are opened from /files.
ENV DATAPORT_DATA_DIR=/data
ENV DATAPORT_QUERY_FILE_DIR=/files
VOLUME /data
EXPOSE 8080

USER dataport
ENTRYPOINT [ "/dataport" ]

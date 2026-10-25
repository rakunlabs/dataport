ARG ALPINE=alpine:3.24.3

FROM $ALPINE

RUN apk --no-cache --no-progress add tzdata ca-certificates \
    && adduser -D -H -u 10001 dataport \
    && mkdir -p /data && chown dataport /data

# GoReleaser puts each platform's binary under linux/<arch>/ in the context.
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/dataport /

# The SQLite store and the generated secret key live here; mount a volume.
ENV DATAPORT_DATA_DIR=/data
VOLUME /data
EXPOSE 8080

USER dataport
ENTRYPOINT [ "/dataport" ]

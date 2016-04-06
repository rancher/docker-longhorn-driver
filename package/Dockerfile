FROM ubuntu:16.04

RUN apt-get update && apt-get install -y curl

RUN curl -sSL -o share-mnt https://github.com/rancher/runc/releases/download/share-mnt-v0.0.3/share-mnt && \
    chmod u+x share-mnt && mv share-mnt /usr/bin

COPY docker-longhorn-driver /usr/bin
COPY launch /usr/bin

ENTRYPOINT ["launch"]
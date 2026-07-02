FROM golang:1.25 AS builder
ENV PROTOBUF_VERSION 21.12
ENV ARCH linux-x86_64
COPY . /root/src
RUN apt-get update && \
    apt-get install -y unzip wget && \
    curl -SLo protoc.zip https://github.com/google/protobuf/releases/download/v$PROTOBUF_VERSION/protoc-$PROTOBUF_VERSION-$ARCH.zip && \
    unzip -d /usr/local protoc.zip && \
    rm protoc.zip && \
    curl --proto '=https' --tlsv1.2 -sSf https://just.systems/install.sh | bash -s -- --to /usr/local/bin && \
    cd /root/src && \
    just

# Rendering happens in-process in `hercules report` (or via the Go `labours`
# binary); fonts are embedded, so no Python or extra runtime deps are needed.
# The former Python labours package was removed from the repo (Phase 9); the
# Go `labours` binary is its drop-in replacement.
FROM ubuntu:22.04
COPY --from=builder /root/src/hercules /usr/local/bin
COPY --from=builder /root/src/labours /usr/local/bin
ENV LC_ALL C.UTF-8
RUN apt-get update && \
    apt-get upgrade -y && \
    apt-get install -y --no-install-suggests --no-install-recommends ca-certificates && \
    printf '#!/bin/bash\n\necho\necho "\t$@"\necho\n' > /browser && \
    chmod +x /browser && \
    rm -rf /usr/share/doc /usr/share/man && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

EXPOSE 8000
ENV BROWSER /browser
ENV COUPLES_SERVER_TIME 7200

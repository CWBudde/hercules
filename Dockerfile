FROM --platform=${BUILDPLATFORM} golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

# The renderer's supported default is pure Go. Building with the target
# variables supplied by BuildKit makes one Dockerfile work for every platform
# in the multi-architecture image.
ENV CGO_ENABLED=0
ENV GOFLAGS=-mod=mod

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/cwbudde/hercules.BinaryGitHash=${COMMIT}" \
    -o /out/hercules ./cmd/hercules
RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/labours ./cmd/labours
RUN install -d -m 0755 -o 65532 -g 65532 /runtime-tmp && \
    touch --date=@0 /out/hercules /out/labours /runtime-tmp

# A cgo-free binary needs only CA roots at runtime. Scratch removes mutable
# package-manager state and avoids a root-capable shell in the release image.
FROM scratch

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Hercules" \
      org.opencontainers.image.description="Git repository analysis and visualization" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/cwbudde/hercules"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/hercules /usr/local/bin/hercules
COPY --from=builder /out/labours /usr/local/bin/labours
COPY --from=builder --chown=65532:65532 /runtime-tmp /tmp

ENV HOME=/tmp
WORKDIR /tmp
USER 65532:65532

CMD ["hercules", "--help"]

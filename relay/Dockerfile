# syntax=docker/dockerfile:1

# The build context is this relay/ directory alone. The service has no module
# dependencies outside it, so no sibling source tree is copied.
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags="-s -w" \
    -o /out/isoform-relay ./cmd/isoform-relay

FROM alpine:3.24
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 isoform \
    && adduser -S -D -H -u 10001 -G isoform isoform \
    && mkdir -p /var/lib/isoform \
    && chown 10001:10001 /var/lib/isoform \
    && chmod 0700 /var/lib/isoform
COPY --from=build --chmod=0555 /out/isoform-relay /usr/local/bin/isoform-relay
USER 10001:10001
WORKDIR /var/lib/isoform
# Loopback by default. Compose overrides the address and supplies TLS files.
ENV ISOFORM_RELAY_ADDR=127.0.0.1:8791 \
    ISOFORM_RELAY_STATE_DIR=/var/lib/isoform
# Probe the scheme the listener uses, so TLS mode does not log a handshake error
# on every check. The probe runs on loopback at the configured port.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["sh", "-c", "port=${ISOFORM_RELAY_ADDR##*:}; scheme=http; if [ -n \"$ISOFORM_RELAY_TLS_CERT\" ]; then scheme=https; fi; wget -q -T 2 --no-check-certificate -O /dev/null \"$scheme://127.0.0.1:$port/v1/health\""]
ENTRYPOINT ["/usr/local/bin/isoform-relay"]

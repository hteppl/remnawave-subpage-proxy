# syntax=docker/dockerfile:1

FROM golang:1.27-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/hteppl/remnawave-subpage-proxy/internal/version.Version=${VERSION} \
        -X github.com/hteppl/remnawave-subpage-proxy/internal/version.Commit=${COMMIT} \
        -X github.com/hteppl/remnawave-subpage-proxy/internal/version.Date=${BUILD_DATE}" \
      -o /out/subpage-proxy ./cmd/subpage-proxy

FROM alpine:3.24

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="remnawave-subpage-proxy" \
      org.opencontainers.image.description="Automatically fill variables in Remnawave (https://docs.rw) subscription params." \
      org.opencontainers.image.source="https://github.com/hteppl/remnawave-subpage-proxy" \
      org.opencontainers.image.url="https://hub.docker.com/r/hteppl/remnawave-subpage-proxy" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# The IANA timezone database is compiled into the binary; only TLS roots are needed.
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 -s /sbin/nologin subpage

COPY --from=build /out/subpage-proxy /usr/local/bin/subpage-proxy

USER 10001:10001
WORKDIR /app

# CONFIG_PATH is left unset so /app/config.yaml stays optional.
ENV APP_PORT=3020 \
    HEALTH_PORT=3021

EXPOSE 3020

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/subpage-proxy", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/subpage-proxy"]

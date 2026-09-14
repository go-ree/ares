# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine3.23 AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

COPY main.go ./
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go build -trimpath -ldflags="-s -w" -o /out/ares ./main.go

FROM alpine:3.24.1

RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates tzdata \
    && addgroup -S ares \
    && adduser -S -G ares ares \
    && mkdir -p /app/config /var/log/ares \
    && chown -R ares:ares /app /var/log/ares

WORKDIR /app
ENV TZ=Asia/Shanghai

COPY --from=builder --chown=ares:ares /out/ares /app/ares
COPY --chown=ares:ares config/docker.yaml /app/config/default.yaml

USER ares
EXPOSE 8080

ENTRYPOINT ["/app/ares"]
CMD ["serve"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/health/live | grep -q '^OK$' || exit 1

# syntax=docker/dockerfile:1

FROM golang:1.26.7-alpine3.23 AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY docs ./docs
COPY internal ./internal
RUN go build -trimpath -ldflags="-s -w" -o /out/ares ./main.go

FROM alpine:3.23.5

RUN apk add --no-cache ca-certificates tzdata \
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

CMD ["/app/ares"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/health/live | grep -q '^OK$' || exit 1

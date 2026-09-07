# syntax=docker/dockerfile:1

FROM node:24.20.0-alpine3.23 AS builder

WORKDIR /app
RUN --mount=type=cache,target=/root/.npm \
  for attempt in 1 2 3; do \
    npm install --global npm@11.19.1 \
      --fetch-retries=5 \
      --fetch-retry-mintimeout=2000 \
      --fetch-retry-maxtimeout=10000 \
      --fetch-timeout=120000 && \
      test "$(npm --version)" = "11.19.1" && exit 0; \
    echo "npm install attempt ${attempt} failed, retrying" >&2; \
  done; \
  exit 1

COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
  for attempt in 1 2 3; do \
    npm ci \
      --fetch-retries=5 \
      --fetch-retry-mintimeout=2000 \
      --fetch-retry-maxtimeout=10000 \
      --fetch-timeout=120000 && \
      node -e "import('rolldown')" && exit 0; \
    echo "npm ci attempt ${attempt} failed, retrying" >&2; \
  done; \
  exit 1

COPY . .
RUN npm run type-check && npm run build:prod

FROM nginxinc/nginx-unprivileged:1.30.3-alpine3.23

USER root
RUN apk upgrade --no-cache
USER 101

ENV NGINX_ENTRYPOINT_QUIET_LOGS=1

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY security-headers.conf /etc/nginx/ares-security-headers.conf
COPY --from=builder /app/dist/ /usr/share/nginx/html/

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/inside/checkup || exit 1

# syntax=docker/dockerfile:1

FROM node:24.20.0-alpine3.23 AS builder

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run type-check && npm run build:prod

FROM nginxinc/nginx-unprivileged:1.30.3-alpine3.23

ENV NGINX_ENTRYPOINT_QUIET_LOGS=1

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist/ /usr/share/nginx/html/

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/inside/checkup || exit 1

# syntax=docker/dockerfile:1.7
FROM node:22.23.1-alpine AS web-builder
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY app ./app
COPY web ./web
COPY vite.frontend.config.ts tsconfig.json ./
RUN npm run build:web

FROM golang:1.26.5-alpine AS go-builder
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/meovv-mail ./cmd/meovv-mail && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mailctl ./cmd/mailctl

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 2001 meovv && adduser -S -D -H -u 2001 -G meovv meovv
COPY --from=go-builder /out/meovv-mail /usr/local/bin/meovv-mail
COPY --from=go-builder /out/mailctl /usr/local/bin/mailctl
COPY --from=web-builder /src/web-dist /app/web
RUN mkdir -p /var/lib/meovv-mail && chown -R meovv:meovv /var/lib/meovv-mail
USER meovv
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/meovv-mail"]

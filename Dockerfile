# ---- Build Stage ----
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Download dependencies first (layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o all2api ./cmd/all2api

# ---- Runtime Stage ----
FROM alpine:3.20

WORKDIR /app

# Runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/all2api /app/all2api
COPY config.yaml.example /app/config.yaml.example

# Expose default port
EXPOSE 8848

# Config directory (writable, for auto-generated config)
VOLUME ["/app/config"]

# Entrypoint: auto-copy config.yaml.example if config.yaml doesn't exist
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

ENTRYPOINT ["/app/docker-entrypoint.sh"]

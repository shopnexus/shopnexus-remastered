# Stage 1: Build the application
FROM golang:1.24.3-alpine3.21 AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go.mod and go.sum first for better layer caching
COPY go.mod go.sum ./

# Download dependencies (this layer will be cached if go.mod/go.sum don't change)
RUN go mod download

# Copy source code
COPY . .

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o server .

# Stage 2: Create minimal runtime image
FROM scratch

# Copy timezone data and certificates from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary
COPY --from=builder /app/server /server

# Copy configs and environment variables
COPY config ./config
COPY .env ./.env

# Use a fixed port instead of environment variable
EXPOSE 50051

# Run as non-root user (if your app supports it)
USER 65534:50051

# Run the server
ENTRYPOINT ["/server"]

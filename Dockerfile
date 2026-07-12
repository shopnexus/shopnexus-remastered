# Stage 1: build the binary + collect the runtime config tree.
FROM golang:alpine AS builder

# Build dependencies
RUN apk add --no-cache ca-certificates git tzdata

WORKDIR /app

# Cache deps first (layer reused if go.mod/go.sum unchanged)
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOEXPERIMENT=greenteagc go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o /out/server ./cmd/server

# migrate is a self-contained binary (migrations are go:embed'd) run by the
# k8s migration Job to bootstrap the DB before the server starts.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOEXPERIMENT=greenteagc go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o /out/migrate ./cmd/migrate

# The app loads YAML config at runtime relative to its working dir
# (config/config.default.yml). Collect only the default into /out — dev secrets
# (config.dev.yml) stay out of the image; prod overrides come from APP_* env.
RUN cd /app && cp --parents config/config.default.yml /out/

# Stage 2: minimal distroless runtime.
FROM gcr.io/distroless/static:nonroot

WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/config /app/config

# 5005 HTTP/API · 8082 restate service endpoint · 8083 best-effort
EXPOSE 5005 8082 8083

USER nonroot:nonroot
ENTRYPOINT ["/app/server"]

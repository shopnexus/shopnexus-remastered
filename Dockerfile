# Builds the gateway and migrate binaries; the compose `gateway`/`migrate`
# services pick which one to run. Static build -> distroless runtime.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /gateway
COPY --from=build /out/migrate /migrate
ENTRYPOINT ["/gateway"]

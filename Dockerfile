# One Dockerfile, three stages. CI builds `runtime` explicitly (see build.yml) so a
# stage added later can never leak the dev toolchain into the published image.
FROM golang:1.27rc2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -o /out/embedder ./cmd/embedder \
 && CGO_ENABLED=0 go build -o /out/mockspec ./cmd/mockspec \
 && CGO_ENABLED=0 go build -o /out/seed ./cmd/seed

# Hot reload for `docker compose --profile dev up`. Deliberately NOT derived from
# `build`: source arrives as a bind mount, so a COPY here would just be shadowed.
FROM golang:1.27rc2 AS dev
WORKDIR /src
RUN go install github.com/air-verse/air@latest
CMD ["air"]

# Binaries the compose `gateway`/`migrate`/`embedder`/`mockspec`/`seed` services pick between.
#
# cmd/seed used to be left out because it read a four-megabyte scrape off the disk. It no longer
# does — its dataset is embedded and its photographs are drawn at run time — and it now has to be
# in the image for two reasons it cannot work around: the module DSNs name compose service
# hostnames that only resolve inside the network, and the photographs have to be written into the
# same volume the gateway serves objects from. It is behind its own compose profile, so nothing
# runs it by accident, and it refuses to load twice or to wipe without being told twice.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gateway /gateway
COPY --from=build /out/migrate /migrate
COPY --from=build /out/embedder /embedder
COPY --from=build /out/mockspec /mockspec
COPY --from=build /out/seed /seed
ENTRYPOINT ["/gateway"]

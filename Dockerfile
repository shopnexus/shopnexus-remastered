# One Dockerfile, three stages. CI builds `runtime` explicitly (see build.yml) so a
# stage added later can never leak the dev toolchain into the published image.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -o /out/embedder ./cmd/embedder \
 && CGO_ENABLED=0 go build -o /out/mockspec ./cmd/mockspec

# Hot reload for `docker compose --profile dev up`. Deliberately NOT derived from
# `build`: source arrives as a bind mount, so a COPY here would just be shadowed.
FROM golang:1.26 AS dev
WORKDIR /src
RUN go install github.com/air-verse/air@latest
CMD ["air"]

# Binaries the compose `gateway`/`migrate`/`embedder`/`mockspec` services pick between.
# cmd/seed is deliberately absent: it reads assets/data.json from disk, and a production image
# has no business carrying four megabytes of development data.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gateway /gateway
COPY --from=build /out/migrate /migrate
COPY --from=build /out/embedder /embedder
COPY --from=build /out/mockspec /mockspec
ENTRYPOINT ["/gateway"]

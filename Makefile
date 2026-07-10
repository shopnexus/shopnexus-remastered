.PHONY: run dev generate pgtempl sqlcgen nullmarshal migrate seed build schema erdiagram cloc cloc-modules test test-order openapi openapi-check

dev:
	air

run:
	go run ./cmd/server/

build:
	go build -o bin/server ./cmd/server/

generate:
	go generate ./...

pgtempl:
	go run ./cmd/pgtempl/ -module all -skip-schema-prefix -single-file=generated_queries.sql

sqlcgen: pgtempl
	sqlc generate
	go run ./cmd/nullmarshal/
	go run ./cmd/pgtempl/ -module all -skip-schema-prefix -emit repo

nullmarshal:
	go run ./cmd/nullmarshal/

migrate:
	go run ./cmd/migrate/

seed:
	go run ./cmd/seed/

schema:
	go run ./cmd/erdiagram/

# Regenerate the OpenAPI spec (internal/openapi) from handler annotations.
# --parseInternal: our code lives under internal/. Type overrides in .swaggo.
openapi:
	go tool swag init -g cmd/server/main.go -d ./ \
		--parseInternal --parseDepth 2 \
		--overridesFile .swaggo -o internal/openapi
	go tool swag fmt -d ./ -g cmd/server/main.go
	go run ./cmd/openapiconv/   # swagger 2.0 -> openapi 3.0 (openapi.v3.json) for Mintlify

# Copy the OpenAPI 3.0 spec into the sibling docs repo (Mintlify reads it).
openapi-docs: openapi
	@test -d ../docs/docs/api && cp internal/openapi/openapi.v3.json ../docs/docs/api/openapi.json \
		&& echo "copied openapi.v3.json -> ../docs/docs/api/openapi.json" \
		|| echo "sibling docs repo (../docs/docs/api) not found — skipped"

# CI guard: regenerate and fail if the committed spec is stale (drift check).
openapi-check: openapi
	@git diff --exit-code -- internal/openapi \
		|| (echo "OpenAPI spec is stale — run 'make openapi' and commit."; exit 1)

test:
	go test ./...

test-order:
	go test ./internal/module/order/biz/...

cloc:
	@find . -type f -name '*.go' \
		-not -path './vendor/*' \
		-not -path './bin/*' \
		-exec grep -L '^// Code generated' {} + \
		| xargs cloc

cloc-modules:
	@for m in internal/module/*/; do \
		name=$$(basename $$m); \
		files=$$(find $$m -type f -name '*.go' -exec grep -L '^// Code generated' {} + 2>/dev/null); \
		[ -z "$$files" ] && continue; \
		count=$$(echo "$$files" | wc -l); \
		loc=$$(echo "$$files" | xargs cat 2>/dev/null | grep -cvE '^\s*(//|$$)'); \
		printf "%-12s %3d files  %6d loc\n" "$$name" "$$count" "$$loc"; \
	done | sort -k4 -rn
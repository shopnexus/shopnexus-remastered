.PHONY: run dev generate pgtempl sqlcgen nullmarshal genenum migrate seed seedcmt build register schema erdiagram cloc cloc-modules test test-order

# Downgrade protobuf registration conflict to warning (restate-sdk vs milvus share "internal.proto")
export GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn

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

nullmarshal:
	go run ./cmd/nullmarshal/

# Regen Valid() + NullXxx{Value,Valid} wrappers for domain string enums (model/*.go //go:generate directives)
genenum:
	go generate ./internal/module/*/model/

migrate:
	go run ./cmd/migrate/

seed:
	go run ./cmd/seed/

schema:
	go run ./cmd/erdiagram/

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
.PHONY: run dev generate pgtempl sqlcgen nullmarshal migrate seed build schema erdiagram cloc cloc-modules test test-order

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
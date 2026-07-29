package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type migration struct {
	name string
	sql  string
}

func readMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration file %q: %w", e.Name(), err)
		}
		out = append(out, migration{name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// Migrate applies the .sql files in fsys (filename order) idempotently, tracking
// applied versions in schema_migrations. Intended to run as a separate step
// (cmd/migrate) — the application does not migrate at startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	migs, err := readMigrations(fsys)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
	    version    TEXT PRIMARY KEY,
	    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	for _, m := range migs {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = @v)`,
			pgx.NamedArgs{"v": m.name}).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %q applied: %w", m.name, err)
		}
		if exists {
			continue
		}
		if _, err := pool.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("apply migration %q: %w", m.name, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES (@v)`,
			pgx.NamedArgs{"v": m.name}); err != nil {
			return fmt.Errorf("record migration %q: %w", m.name, err)
		}
	}
	return nil
}

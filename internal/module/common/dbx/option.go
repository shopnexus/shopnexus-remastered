package dbx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
)

// Options reads and writes the `option` table of one schema — the module owns the category it acts
// on. Every module builds one over its own pool, so the shared code lives here once instead of
// as a copy per module.
type Options struct{ pool *pgxpool.Pool }

func NewOptions(pool *pgxpool.Pool) *Options { return &Options{pool: pool} }

// optionColumns is the row every read here returns. One list, because three queries scan it.
const optionColumns = `id, owner_id, is_enabled, name, description, priority,
                       logo_resource_id, data, type, provider`

// ListEnabled answers the live options of one category, best first — what a client picks from.
//
// `deleted_at IS NULL` is half of live: a retired row is `is_enabled = false`, while one removed
// from the registry outright keeps its flag, and the partial index this query exists for is
// declared over both.
func (s *Options) ListEnabled(ctx context.Context, category string) ([]common.Option, error) {
	const q = `SELECT ` + optionColumns + `
	           FROM option
	           WHERE type = @category AND is_enabled AND deleted_at IS NULL
	           ORDER BY priority DESC, name`
	return s.query(ctx, q, pgx.NamedArgs{"category": category})
}

// ListAll answers every row of a category including the disabled ones — the staff view, where
// "why is this carrier missing from checkout" is the question being asked.
func (s *Options) ListAll(ctx context.Context, category string) ([]common.Option, error) {
	const q = `SELECT ` + optionColumns + `
	           FROM option
	           WHERE type = @category AND deleted_at IS NULL
	           ORDER BY priority DESC, name`
	return s.query(ctx, q, pgx.NamedArgs{"category": category})
}

// Find answers one row whatever its state, so an admin can re-enable what they disabled.
func (s *Options) Find(ctx context.Context, id string) (common.Option, error) {
	const q = `SELECT ` + optionColumns + `
	           FROM option WHERE id = @id AND deleted_at IS NULL`
	rows, err := s.query(ctx, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return common.Option{}, err
	}
	if len(rows) == 0 {
		return common.Option{}, common.ErrOptionNotFound
	}
	return rows[0], nil
}

// Save writes the fields an admin may change. Not the id, the category or the timestamps: the slug
// is permanent because past orders hold it, and moving a row to another category would change what
// a settled payment means.
func (s *Options) Save(ctx context.Context, o common.Option) error {
	const q = `UPDATE option
	           SET is_enabled = @is_enabled, name = @name, description = @description,
	               priority = @priority, provider = @provider
	           WHERE id = @id AND deleted_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, pgx.NamedArgs{
		"id": o.ID, "is_enabled": o.IsEnabled, "name": o.Name,
		"description": o.Description, "priority": o.Priority, "provider": o.Provider,
	})
	if err != nil {
		return fmt.Errorf("db update option: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return common.ErrOptionNotFound
	}
	return nil
}

// Reconcile makes the rows of one provider *exactly* want. Upsert plus a delete of what is no
// longer named, in one transaction, so a provider dropped from the registered list leaves no rows
// for a client to pick from — and so a startup that half-ran leaves nothing half-registered.
//
// Scoped to one provider on purpose: it must never touch the rows an operator wrote by hand.
func (s *Options) Reconcile(ctx context.Context, provider string, want []common.Option) error {
	return InTx(ctx, s.pool, func(tx pgx.Tx) error {
		keep := make([]string, 0, len(want))
		for _, o := range want {
			keep = append(keep, o.ID)
			// The name and the description are refreshed, because they describe what the code
			// does and the code is what changed. `is_enabled` is not: an operator who switched one
			// of these off should not have it switched back on by a restart.
			const upsert = `
				INSERT INTO option (id, is_enabled, name, description, priority, data, type, provider)
				VALUES (@id, TRUE, @name, @description, @priority, '{}', @category, @provider)
				ON CONFLICT (id) DO UPDATE
				SET name = @name, description = @description, priority = @priority,
				    provider = @provider, deleted_at = NULL`
			if _, err := tx.Exec(ctx, upsert, pgx.NamedArgs{
				"id": o.ID, "name": o.Name, "description": o.Description,
				"priority": o.Priority, "category": o.Category, "provider": provider,
			}); err != nil {
				return fmt.Errorf("db upsert option %s: %w", o.ID, err)
			}
		}
		// Deleted outright rather than disabled: these rows are the code's, so one the code no
		// longer defines is not a retired choice an order may still name — it never shipped.
		const prune = `DELETE FROM option WHERE provider = @provider AND id <> ALL(@keep)`
		if _, err := tx.Exec(ctx, prune, pgx.NamedArgs{
			"provider": provider, "keep": keep,
		}); err != nil {
			return fmt.Errorf("db prune %s options: %w", provider, err)
		}
		return nil
	})
}

func (s *Options) query(ctx context.Context, q string, args pgx.NamedArgs) ([]common.Option, error) {
	rows, err := s.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query options: %w", err)
	}
	defer rows.Close()

	var out []common.Option
	for rows.Next() {
		var o common.Option
		if err := rows.Scan(&o.ID, &o.OwnerID, &o.IsEnabled, &o.Name, &o.Description,
			&o.Priority, &o.LogoResourceID, &o.Data, &o.Category, &o.Provider); err != nil {
			return nil, fmt.Errorf("db scan option row: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate options: %w", err)
	}
	return out, nil
}

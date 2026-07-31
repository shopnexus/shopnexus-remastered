package dbx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
)

// Options reads the `option` table of one schema — the module owns the kind it acts on.
type Options struct{ pool *pgxpool.Pool }

func NewOptions(pool *pgxpool.Pool) *Options { return &Options{pool: pool} }

// ListEnabled answers the live options of one type, best first.
func (s *Options) ListEnabled(ctx context.Context, optionType string) ([]common.Option, error) {
	const q = `SELECT id, owner_id, is_enabled, name, description, priority,
	                  logo_resource_id, data, type, provider
	           FROM option
	           WHERE type = @type AND is_enabled
	           ORDER BY priority DESC, name`
	rows, err := s.pool.Query(ctx, q, pgx.NamedArgs{"type": optionType})
	if err != nil {
		return nil, fmt.Errorf("db query options: %w", err)
	}
	defer rows.Close()

	var out []common.Option
	for rows.Next() {
		var o common.Option
		if err := rows.Scan(&o.ID, &o.OwnerID, &o.IsEnabled, &o.Name, &o.Description,
			&o.Priority, &o.LogoResourceID, &o.Data, &o.Type, &o.Provider); err != nil {
			return nil, fmt.Errorf("db scan option row: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate options: %w", err)
	}
	return out, nil
}

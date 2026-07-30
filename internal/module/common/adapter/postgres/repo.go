// Package postgres implements the common port.Repository using pgx named args.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/domain"
	"shopnexus/internal/module/common/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) InsertResource(ctx context.Context, res *domain.Resource) error {
	const q = `INSERT INTO resource (uploaded_by_id, provider, object_key, mime, size, metadata, checksum)
	           VALUES (@uploaded_by_id, @provider, @object_key, @mime, @size, @metadata, @checksum)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"uploaded_by_id": nullID(res.UploadedByID),
		"provider":       res.Provider,
		"object_key":     res.ObjectKey,
		"mime":           res.Mime,
		"size":           res.Size,
		"metadata":       res.Metadata,
		"checksum":       nullText(res.Checksum),
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&res.ID, &res.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrDuplicateObject
		}
		return fmt.Errorf("db insert resource: %w", err)
	}
	return nil
}

// FindResources reads a batch. Only live, completed uploads are returned: an
// unconfirmed resource has no bytes behind it yet, and a soft-deleted row only exists
// until the reaper has removed the object.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]domain.Resource, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `SELECT id, COALESCE(uploaded_by_id, 0), provider, object_key, mime, size,
	                  metadata, COALESCE(checksum, ''), created_at
	           FROM resource
	           WHERE id = ANY(@ids) AND completed_at IS NOT NULL AND deleted_at IS NULL`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": ids})
	if err != nil {
		return nil, fmt.Errorf("db query resources: %w", err)
	}
	defer rows.Close()

	var out []domain.Resource
	for rows.Next() {
		var res domain.Resource
		if err := rows.Scan(&res.ID, &res.UploadedByID, &res.Provider, &res.ObjectKey, &res.Mime,
			&res.Size, &res.Metadata, &res.Checksum, &res.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan resource row: %w", err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate resources: %w", err)
	}
	return out, nil
}

func (r *Repo) ListEnabledOptions(ctx context.Context, optionType string) ([]domain.Option, error) {
	const q = `SELECT id, COALESCE(owner_id::text, ''), is_enabled, name, description, priority,
	                  COALESCE(logo_resource_id::text, ''), data, type, provider
	           FROM option
	           WHERE type = @type AND is_enabled
	           ORDER BY priority DESC, name`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"type": optionType})
	if err != nil {
		return nil, fmt.Errorf("db query options: %w", err)
	}
	defer rows.Close()

	var out []domain.Option
	for rows.Next() {
		var o domain.Option
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

// nullID/nullText keep optional columns NULL instead of storing a zero value.
// The zero id means "no id" (shared/id), and identity columns never produce 0.
func nullID(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation reports whether err is Postgres 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

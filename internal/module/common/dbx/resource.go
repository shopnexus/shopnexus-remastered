package dbx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
)

// Resources reads and writes the `resource` table of one schema. A module holds one built on
// its own pool, so the rows it uploaded are its own — and when it moves to its own database
// they move with it. In dev every pool points at the same server.
type Resources struct{ pool *pgxpool.Pool }

func NewResources(pool *pgxpool.Pool) *Resources { return &Resources{pool: pool} }

func (s *Resources) Insert(ctx context.Context, res *common.Resource) error {
	const q = `INSERT INTO resource (uploaded_by_id, provider, object_key, mime, size, metadata, checksum)
	           VALUES (@uploaded_by_id, @provider, @object_key, @mime, @size, @metadata, @checksum)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"uploaded_by_id": res.UploadedByID,
		"provider":       res.Provider,
		"object_key":     res.ObjectKey,
		"mime":           res.Mime,
		"size":           res.Size,
		"metadata":       res.Metadata,
		"checksum":       res.Checksum,
	}
	if err := s.pool.QueryRow(ctx, q, args).Scan(&res.ID, &res.CreatedAt); err != nil {
		if IsUniqueViolation(err) {
			return common.ErrDuplicateObject
		}
		return fmt.Errorf("db insert resource: %w", err)
	}
	return nil
}

// Find reads a batch. Only live, completed uploads come back: an unconfirmed resource has no
// bytes behind it yet, and a soft-deleted row exists only until the reaper has removed the
// object. A missing id is simply absent — a row pointing at a deleted resource is a picture
// that does not render, not an error that fails the page.
func (s *Resources) Find(ctx context.Context, ids []int64) ([]common.Resource, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `SELECT id, uploaded_by_id, provider, object_key, mime, size,
	                  metadata, checksum, created_at
	           FROM resource
	           WHERE id = ANY(@ids) AND completed_at IS NOT NULL AND deleted_at IS NULL`
	rows, err := s.pool.Query(ctx, q, pgx.NamedArgs{"ids": ids})
	if err != nil {
		return nil, fmt.Errorf("db query resources: %w", err)
	}
	defer rows.Close()

	var out []common.Resource
	for rows.Next() {
		var res common.Resource
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

package dbx

import (
	"context"
	"fmt"
	"time"

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

// Confirm is the second half of an upload: the bytes are in the store, so the row becomes
// visible to Find. The size and the checksum come from the store rather than the client — a
// client that says it uploaded 3 KB and uploaded 30 MB would otherwise set the record.
//
// Guarded by `completed_at IS NULL`, so a retried confirm is a no-op rather than a second
// timestamp; and scoped to the uploader, because a resource id is guessable and confirming
// somebody else's slot is claiming their upload.
func (s *Resources) Confirm(ctx context.Context, id int64, uploaderID *int64, size int64, checksum *string) (common.Resource, error) {
	const q = `UPDATE resource
	           SET completed_at = CURRENT_TIMESTAMP, size = @size, checksum = @checksum
	           WHERE id = @id AND completed_at IS NULL AND deleted_at IS NULL
	             AND uploaded_by_id IS NOT DISTINCT FROM @uploaded_by_id
	           RETURNING id, uploaded_by_id, provider, object_key, mime, size,
	                     metadata, checksum, created_at`
	args := pgx.NamedArgs{
		"id": id, "uploaded_by_id": uploaderID, "size": size, "checksum": checksum,
	}
	var res common.Resource
	err := s.pool.QueryRow(ctx, q, args).Scan(&res.ID, &res.UploadedByID, &res.Provider,
		&res.ObjectKey, &res.Mime, &res.Size, &res.Metadata, &res.Checksum, &res.CreatedAt)
	if IsNoRows(err) {
		// Either it is not theirs, it is gone, or it was already confirmed. The first two are
		// not-found to this caller and the third is the state they wanted, so the caller reads
		// the row again rather than being told which.
		return common.Resource{}, common.ErrResourceNotFound
	}
	if err != nil {
		return common.Resource{}, fmt.Errorf("db confirm resource: %w", err)
	}
	return res, nil
}

// FindPending reads one uploader's unconfirmed slot, which is what lets a confirm answer the
// object key the store has to be asked about.
func (s *Resources) FindPending(ctx context.Context, id int64, uploaderID *int64) (common.Resource, error) {
	const q = `SELECT id, uploaded_by_id, provider, object_key, mime, size,
	                  metadata, checksum, created_at
	           FROM resource
	           WHERE id = @id AND completed_at IS NULL AND deleted_at IS NULL
	             AND uploaded_by_id IS NOT DISTINCT FROM @uploaded_by_id`
	var res common.Resource
	err := s.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id, "uploaded_by_id": uploaderID}).
		Scan(&res.ID, &res.UploadedByID, &res.Provider, &res.ObjectKey, &res.Mime,
			&res.Size, &res.Metadata, &res.Checksum, &res.CreatedAt)
	if IsNoRows(err) {
		return common.Resource{}, common.ErrResourceNotFound
	}
	if err != nil {
		return common.Resource{}, fmt.Errorf("db scan pending resource: %w", err)
	}
	return res, nil
}

// Abandoned is the reaper's list: slots nobody confirmed, older than the window a client had
// to upload in. It reads the partial index over incomplete rows, so a large history of
// confirmed uploads costs nothing.
func (s *Resources) Abandoned(ctx context.Context, olderThan time.Duration, limit int) ([]common.Resource, error) {
	const q = `SELECT id, uploaded_by_id, provider, object_key, mime, size,
	                  metadata, checksum, created_at
	           FROM resource
	           WHERE completed_at IS NULL AND deleted_at IS NULL
	             AND created_at + @window::interval < CURRENT_TIMESTAMP
	           ORDER BY created_at
	           LIMIT @limit`
	args := pgx.NamedArgs{
		"window": fmt.Sprintf("%d seconds", int(olderThan.Seconds())), "limit": limit,
	}
	rows, err := s.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query abandoned resources: %w", err)
	}
	defer rows.Close()
	var out []common.Resource
	for rows.Next() {
		var res common.Resource
		if err := rows.Scan(&res.ID, &res.UploadedByID, &res.Provider, &res.ObjectKey,
			&res.Mime, &res.Size, &res.Metadata, &res.Checksum, &res.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan abandoned resource: %w", err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate abandoned resources: %w", err)
	}
	return out, nil
}

// Delete soft-deletes a row so nothing new can resolve it. The bytes go separately: the store
// is another system, and a failed object delete must not leave a row that still renders.
func (s *Resources) Delete(ctx context.Context, id int64) error {
	const q = `UPDATE resource SET deleted_at = CURRENT_TIMESTAMP
	           WHERE id = @id AND deleted_at IS NULL`
	if _, err := s.pool.Exec(ctx, q, pgx.NamedArgs{"id": id}); err != nil {
		return fmt.Errorf("db delete resource: %w", err)
	}
	return nil
}

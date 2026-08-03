package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// ListTags pages the dictionary and matches Prefix against the head of the slug — what the
// tag picker types into, served by tag_id_prefix_idx (text_pattern_ops, because a btree
// under a non-C collation will not turn a prefix into a range).
//
// COUNT(*) OVER () brings the total back with the rows, so a page costs one round trip
// rather than a query plus a matching count that can drift from it.
func (r *Repo) ListTags(ctx context.Context, f port.TagFilter) ([]domain.Tag, int64, error) {
	const q = `SELECT id, description, COUNT(*) OVER () AS total_count
	           FROM tag
	           WHERE @prefix = '' OR id LIKE @pattern ESCAPE '\'
	           ORDER BY id
	           LIMIT @limit OFFSET @offset`
	// The LIKE pattern is built from a parameter, never concatenated into the statement, and
	// the prefix is escaped: a bare "%" would match the whole dictionary and lose the index.
	args := pgx.NamedArgs{
		"prefix":  f.Prefix,
		"pattern": escapeLike(f.Prefix) + "%",
		"limit":   f.Limit,
		"offset":  f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query tags: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Tag
		total int64
	)
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.Slug, &t.Description, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan tag row: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate tags: %w", err)
	}
	return out, total, nil
}

// escapeLike makes a user-supplied string a literal inside a LIKE pattern, so a typed "%"
// means a per-cent sign and not "everything".
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}

// PutTag is an upsert on the natural key, which is what makes PUT idempotent.
func (r *Repo) PutTag(ctx context.Context, t domain.Tag) error {
	// Stale on either path: the slug and the description are the tag's whole text, so a new
	// one has no vector yet and a re-described one has the wrong vector.
	const q = `INSERT INTO tag (id, description, embedding_stale_at)
	           VALUES (@id, @description, now())
	           ON CONFLICT (id) DO UPDATE
	             SET description = @description, embedding_stale_at = now()`
	args := pgx.NamedArgs{"id": t.Slug, "description": t.Description}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db upsert tag: %w", err)
	}
	return nil
}

// DeleteTag relies on ON DELETE CASCADE to drop the listing joins with it: a tag that no
// longer exists cannot stay on a listing.
func (r *Repo) DeleteTag(ctx context.Context, slug string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tag WHERE id = @id`, pgx.NamedArgs{"id": slug})
	if err != nil {
		return fmt.Errorf("db delete tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTagNotFound
	}
	return nil
}

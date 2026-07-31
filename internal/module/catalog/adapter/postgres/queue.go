package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/port"
)

// ListModerationQueue is one statement for both halves of the queue: a listing awaiting its
// first publication (status = 'pending') and a live one holding an edit (pending_edit IS NOT
// NULL). listing_moderation_queue_idx covers exactly that predicate.
//
// The price comes from the featured variant in a lateral join rather than a second round
// trip, and COUNT(*) OVER () brings the total back with the rows so a page costs one trip.
func (r *Repo) ListModerationQueue(ctx context.Context, f port.QueueFilter) ([]port.ListingSummary, int64, error) {
	const q = `SELECT l.id, l.account_id, l.slug, l.name, l.status::text, l.condition::text,
	                  l.price_mode::text, l.currency, COALESCE(v.price, 0), l.cached_sold,
	                  l.cached_rating, l.category_id, l.attachments[1],
	                  l.pending_edit IS NOT NULL, l.created_at,
	                  COUNT(*) OVER () AS total_count
	           FROM listing l
	           LEFT JOIN LATERAL (
	             SELECT price FROM variant
	             WHERE listing_id = l.id AND deleted_at IS NULL
	             ORDER BY is_featured DESC, price
	             LIMIT 1
	           ) v ON true
	           WHERE l.deleted_at IS NULL
	             AND (@status = '' OR l.status::text = @status)
	             AND (@status <> '' OR l.status = 'pending' OR l.pending_edit IS NOT NULL)
	             AND (@seller_id = 0 OR l.account_id = @seller_id)
	           ORDER BY l.created_at
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"status":    string(f.Status),
		"seller_id": f.SellerID,
		"limit":     f.Limit,
		"offset":    f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query moderation queue: %w", err)
	}
	defer rows.Close()

	var (
		out   []port.ListingSummary
		total int64
	)
	for rows.Next() {
		var s port.ListingSummary
		if err := rows.Scan(&s.ID, &s.SellerID, &s.Slug, &s.Name, &s.Status, &s.Condition,
			&s.PriceMode, &s.Currency, &s.Price, &s.Sold, &s.Rating, &s.CategoryID,
			&s.CoverID, &s.HasPendingEdit, &s.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan queue row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate moderation queue: %w", err)
	}
	return out, total, nil
}

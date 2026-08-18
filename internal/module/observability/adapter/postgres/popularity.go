package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/observability/domain"
)

// popularityDelta is what one batch of interactions adds to one listing's counters.
type popularityDelta struct {
	score                               float64
	views, clicks, dismisses, purchases int64
}

// ApplyPopularityDeltas groups a batch by listing — one account viewing the same card three
// times in one batch is one statement — and applies each with counterOf's type-to-counter
// mapping. UPDATE-then-INSERT, per addReviewRating's pattern in trust: Postgres checks a
// proposed row's constraints before it detects the conflict, so an upsert carrying a negative
// score delta could fail a check the row was never going to violate on update.
func (r *Repo) ApplyPopularityDeltas(ctx context.Context, events []domain.ListingInteractionEvent) error {
	if len(events) == 0 {
		return nil
	}
	byListing := make(map[int64]*popularityDelta)
	for _, e := range events {
		d, ok := byListing[e.ListingID]
		if !ok {
			d = &popularityDelta{}
			byListing[e.ListingID] = d
		}
		d.score += e.Weight
		switch counterOf(e.Type) {
		case "view":
			d.views++
		case "click":
			d.clicks++
		case "dismiss":
			d.dismisses++
		case "purchase":
			d.purchases++
		}
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		for listingID, d := range byListing {
			if err := applyPopularityDelta(ctx, tx, listingID, *d); err != nil {
				return err
			}
		}
		return nil
	})
}

// counterOf is the vocabulary catalogapi.Interaction* names, read as plain strings so this
// adapter does not import that module's api package for six literals — the same trade-off
// observedTopics already makes for the topic name itself.
func counterOf(interactionType string) string {
	switch interactionType {
	case "view":
		return "view"
	case "click-from-search", "click-from-recommended", "click-from-category":
		return "click"
	case "not-interested", "hidden":
		return "dismiss"
	case "purchase":
		return "purchase"
	default:
		return ""
	}
}

func applyPopularityDelta(ctx context.Context, tx pgx.Tx, listingID int64, d popularityDelta) error {
	const update = `UPDATE listing_popularity
	                SET score = score + @score,
	                    view_count = view_count + @views,
	                    click_count = click_count + @clicks,
	                    dismiss_count = dismiss_count + @dismisses,
	                    purchase_count = purchase_count + @purchases,
	                    updated_at = CURRENT_TIMESTAMP
	                WHERE listing_id = @listing_id`
	args := pgx.NamedArgs{
		"listing_id": listingID, "score": d.score,
		"views": d.views, "clicks": d.clicks, "dismisses": d.dismisses, "purchases": d.purchases,
	}
	tag, err := tx.Exec(ctx, update, args)
	if err != nil {
		return fmt.Errorf("db update listing popularity: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	const insert = `INSERT INTO listing_popularity
	                (listing_id, score, view_count, click_count, dismiss_count, purchase_count)
	                VALUES (@listing_id, @score, @views, @clicks, @dismisses, @purchases)`
	if _, err := tx.Exec(ctx, insert, args); err != nil {
		if dbx.IsUniqueViolation(err) {
			// Two first sightings of the same listing at once: the loser applies its delta to
			// the row the winner just created rather than starting a second aggregate.
			if _, err := tx.Exec(ctx, update, args); err != nil {
				return fmt.Errorf("db update listing popularity: %w", err)
			}
			return nil
		}
		return fmt.Errorf("db insert listing popularity: %w", err)
	}
	return nil
}

// PopularityOf answers 0 for a listing with no rows yet, so a caller never has to branch on
// "found" — a listing nobody has interacted with has a popularity of zero, which is the
// truth.
func (r *Repo) PopularityOf(ctx context.Context, listingIDs []int64) (map[int64]float64, error) {
	out := make(map[int64]float64, len(listingIDs))
	for _, id := range listingIDs {
		out[id] = 0
	}
	if len(listingIDs) == 0 {
		return out, nil
	}
	const q = `SELECT listing_id, score FROM listing_popularity WHERE listing_id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": listingIDs})
	if err != nil {
		return nil, fmt.Errorf("db query listing popularity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, fmt.Errorf("db scan listing popularity: %w", err)
		}
		out[id] = score
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate listing popularity: %w", err)
	}
	return out, nil
}

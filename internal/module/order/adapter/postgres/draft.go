package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
)

const draftColumns = `id, buyer_id, listing_id, spu_snapshot, created_at, cancelled_at,
	       valid_until`

func scanDraft(row pgx.Row) (domain.Draft, error) {
	var (
		d   domain.Draft
		raw []byte
	)
	err := row.Scan(&d.ID, &d.BuyerID, &d.ListingID, &raw, &d.CreatedAt, &d.CancelledAt,
		&d.ValidUntil)
	if dbx.IsNoRows(err) {
		return domain.Draft{}, domain.ErrDraftNotFound
	}
	if err != nil {
		return domain.Draft{}, fmt.Errorf("db scan draft: %w", err)
	}
	snapshot, err := domain.DecodeSnapshot(raw)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("decode draft snapshot: %w", err)
	}
	d.Snapshot = snapshot
	return d, nil
}

func (r *Repo) InsertDraft(ctx context.Context, d *domain.Draft) error {
	const q = `INSERT INTO draft_order (buyer_id, listing_id, spu_snapshot, valid_until)
	           VALUES (@buyer_id, @listing_id, @snapshot, @valid_until)
	           RETURNING id, created_at`
	snapshot, err := d.EncodeSnapshot()
	if err != nil {
		return fmt.Errorf("encode draft snapshot: %w", err)
	}
	args := pgx.NamedArgs{
		"buyer_id": d.BuyerID, "listing_id": d.ListingID,
		"snapshot": snapshot, "valid_until": d.ValidUntil,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&d.ID, &d.CreatedAt); err != nil {
		return fmt.Errorf("db insert draft: %w", err)
	}
	return nil
}

func (r *Repo) FindDraft(ctx context.Context, id, buyerID int64) (domain.Draft, error) {
	const q = `SELECT ` + draftColumns + ` FROM draft_order
	           WHERE id = @id AND buyer_id = @buyer_id`
	args := pgx.NamedArgs{"id": id, "buyer_id": buyerID}
	return scanDraft(r.pool.QueryRow(ctx, q, args))
}

func (r *Repo) ListDrafts(ctx context.Context, buyerID int64, f port.CursorFilter) ([]domain.Draft, error) {
	const q = `SELECT ` + draftColumns + ` FROM draft_order
	           WHERE buyer_id = @buyer_id
	             AND (@before::timestamptz IS NULL
	                  OR (created_at, id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f)
	args := pgx.NamedArgs{
		"buyer_id": buyerID, "before": before, "before_id": beforeID, "limit": limit,
	}
	return r.queryDrafts(ctx, q, args)
}

// ExpiredDrafts is the sweep. A durable timer per draft does the same job; this exists so
// anything a timer lost still gets closed.
func (r *Repo) ExpiredDrafts(ctx context.Context, now time.Time, limit int) ([]domain.Draft, error) {
	const q = `SELECT ` + draftColumns + ` FROM draft_order
	           WHERE cancelled_at IS NULL AND valid_until < @now
	           ORDER BY valid_until
	           LIMIT @limit`
	args := pgx.NamedArgs{"now": now, "limit": limit}
	return r.queryDrafts(ctx, q, args)
}

func (r *Repo) queryDrafts(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Draft, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query drafts: %w", err)
	}
	defer rows.Close()

	var out []domain.Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate drafts: %w", err)
	}
	return out, nil
}

// SaveDraft writes the cancellation. `WHERE cancelled_at IS NULL` is the transition, so
// two concurrent cancels cannot both land.
func (r *Repo) SaveDraft(ctx context.Context, d domain.Draft) error {
	const q = `UPDATE draft_order SET cancelled_at = @cancelled_at
	           WHERE id = @id AND cancelled_at IS NULL`
	args := pgx.NamedArgs{"id": d.ID, "cancelled_at": d.CancelledAt}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update draft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDraftSettled
	}
	return nil
}

const offerColumns = `id, listing_id, variant_id, author_id, buyer_id, seller_id,
	       status::text, quantity, total, reason, payment_session_id, created_at, expires_at`

func scanOffer(row pgx.Row) (domain.Offer, error) {
	var o domain.Offer
	err := row.Scan(&o.ID, &o.ListingID, &o.VariantID, &o.AuthorID, &o.BuyerID, &o.SellerID,
		&o.Status, &o.Quantity, &o.Total, &o.Reason, &o.PaymentSessionID, &o.CreatedAt,
		&o.ExpiresAt)
	if dbx.IsNoRows(err) {
		return domain.Offer{}, domain.ErrOfferNotFound
	}
	if err != nil {
		return domain.Offer{}, fmt.Errorf("db scan offer: %w", err)
	}
	return o, nil
}

// InsertOffer opens a negotiation. The partial unique index holds one-active-per-pair, so a
// second open one is a conflict rather than a duplicate row.
func (r *Repo) InsertOffer(ctx context.Context, o *domain.Offer) error {
	const q = `INSERT INTO offer (listing_id, variant_id, author_id, buyer_id, seller_id,
	                     status, quantity, total, reason, expires_at)
	           VALUES (@listing_id, @variant_id, @author_id, @buyer_id, @seller_id,
	                   @status, @quantity, @total, @reason, @expires_at)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"listing_id": o.ListingID, "variant_id": o.VariantID, "author_id": o.AuthorID,
		"buyer_id": o.BuyerID, "seller_id": o.SellerID, "status": o.Status,
		"quantity": o.Quantity, "total": o.Total, "reason": o.Reason,
		"expires_at": o.ExpiresAt,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&o.ID, &o.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrOfferAlreadyOpen
		}
		return fmt.Errorf("db insert offer: %w", err)
	}
	return nil
}

func (r *Repo) FindOffer(ctx context.Context, id int64) (domain.Offer, error) {
	const q = `SELECT ` + offerColumns + ` FROM offer WHERE id = @id`
	return scanOffer(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

func (r *Repo) FindActiveOffer(ctx context.Context, buyerID, variantID int64) (domain.Offer, error) {
	const q = `SELECT ` + offerColumns + ` FROM offer
	           WHERE buyer_id = @buyer_id AND variant_id = @variant_id AND status = 'active'`
	args := pgx.NamedArgs{"buyer_id": buyerID, "variant_id": variantID}
	return scanOffer(r.pool.QueryRow(ctx, q, args))
}

func (r *Repo) ListOffers(ctx context.Context, f port.OfferFilter) ([]domain.Offer, error) {
	const q = `SELECT ` + offerColumns + ` FROM offer
	           WHERE (buyer_id = @account_id OR seller_id = @account_id)
	             AND (@status::text IS NULL OR status::text = @status::text)
	             AND (@before::timestamptz IS NULL
	                  OR (created_at, id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"account_id": f.AccountID, "status": nullText(f.Status),
		"before": before, "before_id": beforeID, "limit": limit,
	}
	return r.queryOffers(ctx, q, args)
}

// ExpiredOffers is the sweep behind the per-offer timer.
func (r *Repo) ExpiredOffers(ctx context.Context, now time.Time, limit int) ([]domain.Offer, error) {
	const q = `SELECT ` + offerColumns + ` FROM offer
	           WHERE status = 'active' AND expires_at < @now
	           ORDER BY expires_at
	           LIMIT @limit`
	return r.queryOffers(ctx, q, pgx.NamedArgs{"now": now, "limit": limit})
}

func (r *Repo) queryOffers(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Offer, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query offers: %w", err)
	}
	defer rows.Close()

	var out []domain.Offer
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate offers: %w", err)
	}
	return out, nil
}

// SaveOffer writes the terms and the status. `WHERE status = 'active'` is the transition:
// two people accepting or countering at once cannot both land.
func (r *Repo) SaveOffer(ctx context.Context, o domain.Offer) error {
	const q = `UPDATE offer
	           SET status = @status, author_id = @author_id, quantity = @quantity,
	               total = @total, reason = @reason, expires_at = @expires_at,
	               payment_session_id = @payment_session_id
	           WHERE id = @id AND status::text = ANY(@from::text[])`
	args := pgx.NamedArgs{
		"id": o.ID, "status": o.Status, "author_id": o.AuthorID, "quantity": o.Quantity,
		"total": o.Total, "reason": o.Reason, "expires_at": o.ExpiresAt,
		"payment_session_id": o.PaymentSessionID,
		// A negotiation moves from `active`, and an accepted one still expires — so the guard is
		// the pair rather than `active` alone, and the checkout claim above is what stops an
		// accepted offer being re-agreed.
		"from": []string{domain.OfferActive, domain.OfferAccepted},
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update offer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOfferSettled
	}
	return nil
}

// ClaimOfferCheckout is the buyer turning agreed terms into an order, and the write is the claim:
// `payment_session_id IS NULL` means one of two concurrent "create order now" presses opens a
// checkout and the other is refused, rather than two sessions for one negotiated price.
func (r *Repo) ClaimOfferCheckout(ctx context.Context, o domain.Offer) error {
	const q = `UPDATE offer SET payment_session_id = @payment_session_id
	           WHERE id = @id AND status = 'accepted' AND payment_session_id IS NULL`
	args := pgx.NamedArgs{"id": o.ID, "payment_session_id": o.PaymentSessionID}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db claim offer checkout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOfferSettled
	}
	return nil
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

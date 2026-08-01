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

// ExpiredOffers is the sweep behind the per-offer timer. Both waits, because both are the same
// fact: a standing proposal nobody answered and an agreed price nobody checked out. A
// `checked-out` offer is left alone — its clock is the payment session's.
func (r *Repo) ExpiredOffers(ctx context.Context, now time.Time, limit int) ([]domain.Offer, error) {
	const q = `SELECT ` + offerColumns + ` FROM offer
	           WHERE status IN ('active', 'accepted') AND expires_at < @now
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

// SaveOffer writes the terms and the status. `from` is the status the entity moved out of, and
// the guard is what makes a stale read lose: a counter that read `active` cannot land on a row
// somebody has since had accepted, so an acceptance is never silently overwritten.
//
// It does not write `payment_session_id`. That column belongs to the checkout, and a write from a
// read taken before the claim would blank it — which would let a second checkout open.
func (r *Repo) SaveOffer(ctx context.Context, o domain.Offer, from []string) error {
	const q = `UPDATE offer
	           SET status = @status, author_id = @author_id, quantity = @quantity,
	               total = @total, reason = @reason, expires_at = @expires_at
	           WHERE id = @id AND status::text = ANY(@from::text[])`
	args := pgx.NamedArgs{
		"id": o.ID, "status": o.Status, "author_id": o.AuthorID, "quantity": o.Quantity,
		"total": o.Total, "reason": o.Reason, "expires_at": o.ExpiresAt,
		"from": from,
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

// ClaimOfferCheckout is the buyer taking agreed terms off the table to pay for them, and the
// status *is* the claim: `WHERE status = 'accepted'` means one of two concurrent "create order
// now" presses proceeds and the other is refused.
//
// Taken before the payment session is opened, not after. Claiming last would let both presses
// open a session — and a second paid session on one negotiation is money the escrow cannot
// account for, because the hold is keyed on the order.
func (r *Repo) ClaimOfferCheckout(ctx context.Context, offerID int64, now time.Time) error {
	const q = `UPDATE offer SET status = 'checked-out'
	           WHERE id = @id AND status = 'accepted' AND expires_at > @now`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": offerID, "now": now})
	if err != nil {
		return fmt.Errorf("db claim offer checkout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOfferSettled
	}
	return nil
}

// ReleaseOfferCheckout hands the claim back when the checkout could not be opened — an
// unreachable ledger or a carrier that would not price the parcel. The buyer retries inside the
// window they still have, rather than having to negotiate the price again.
func (r *Repo) ReleaseOfferCheckout(ctx context.Context, offerID int64) error {
	const q = `UPDATE offer SET status = 'accepted'
	           WHERE id = @id AND status = 'checked-out' AND payment_session_id IS NULL`
	if _, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": offerID}); err != nil {
		return fmt.Errorf("db release offer checkout: %w", err)
	}
	return nil
}

// AttachOfferSession records which checkout the claim became. Guarded on the claim, so it cannot
// write a session onto an offer that was never claimed.
func (r *Repo) AttachOfferSession(ctx context.Context, offerID, sessionID int64) error {
	const q = `UPDATE offer SET payment_session_id = @payment_session_id
	           WHERE id = @id AND status = 'checked-out' AND payment_session_id IS NULL`
	args := pgx.NamedArgs{"id": offerID, "payment_session_id": sessionID}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db attach offer session: %w", err)
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

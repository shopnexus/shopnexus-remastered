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

const itemColumns = `id, draft_id, offer_id, order_id, buyer_id, seller_id, listing_id,
	       variant_id, address, note, currency, quantity, transport_option, total_amount,
	       payment_session_id, cancelled_at, cancelled_by_id, created_at`

func scanItem(row pgx.Row) (domain.Item, error) {
	var (
		i       domain.Item
		address []byte
		note    *string
	)
	err := row.Scan(&i.ID, &i.DraftID, &i.OfferID, &i.OrderID, &i.BuyerID, &i.SellerID,
		&i.ListingID, &i.VariantID, &address, &note, &i.Currency, &i.Quantity,
		&i.TransportOption, &i.TotalAmount, &i.PaymentSessionID, &i.CancelledAt,
		&i.CancelledByID, &i.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Item{}, domain.ErrItemNotFound
	}
	if err != nil {
		return domain.Item{}, fmt.Errorf("db scan item: %w", err)
	}
	if note != nil {
		i.Note = *note
	}
	snapshot, err := domain.DecodeAddress(address)
	if err != nil {
		return domain.Item{}, fmt.Errorf("decode item address: %w", err)
	}
	i.Address = snapshot
	return i, nil
}

// InsertItems writes a checkout's lines in one transaction: a partial checkout is a buyer
// charged for something they have no row for.
func (r *Repo) InsertItems(ctx context.Context, items []*domain.Item) error {
	if len(items) == 0 {
		return nil
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO item (draft_id, offer_id, buyer_id, seller_id, listing_id,
		                     variant_id, address, note, currency, quantity,
		                     transport_option, total_amount, payment_session_id)
		           VALUES (@draft_id, @offer_id, @buyer_id, @seller_id, @listing_id,
		                   @variant_id, @address, @note, @currency, @quantity,
		                   @transport_option, @total_amount, @payment_session_id)
		           RETURNING id, created_at`
		for _, i := range items {
			address, err := domain.EncodeAddress(i.Address)
			if err != nil {
				return fmt.Errorf("encode item address: %w", err)
			}
			args := pgx.NamedArgs{
				"draft_id": i.DraftID, "offer_id": i.OfferID, "buyer_id": i.BuyerID,
				"seller_id": i.SellerID, "listing_id": i.ListingID, "variant_id": i.VariantID,
				"address": address, "note": dbx.NullText(i.Note), "currency": i.Currency,
				"quantity": i.Quantity, "transport_option": i.TransportOption,
				"total_amount": i.TotalAmount, "payment_session_id": i.PaymentSessionID,
			}
			if err := tx.QueryRow(ctx, q, args).Scan(&i.ID, &i.CreatedAt); err != nil {
				return fmt.Errorf("db insert item: %w", err)
			}
		}
		return nil
	})
}

func (r *Repo) FindItem(ctx context.Context, id int64) (domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM item WHERE id = @id`
	return scanItem(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

func (r *Repo) ListItems(ctx context.Context, f port.ItemFilter) ([]domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM item
	           WHERE (@buyer_id = 0 OR buyer_id = @buyer_id)
	             AND (@seller_id = 0 OR seller_id = @seller_id)
	             AND (NOT @pending_only OR (order_id IS NULL AND cancelled_at IS NULL))
	             AND (@before::timestamptz IS NULL
	                  OR (created_at, id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"buyer_id": f.BuyerID, "seller_id": f.SellerID, "pending_only": f.PendingOnly,
		"before": before, "before_id": beforeID, "limit": limit,
	}
	return r.queryItems(ctx, q, args)
}

// ItemsByPaymentSession is the webhook's first lookup: which lines did this session pay for.
func (r *Repo) ItemsByPaymentSession(ctx context.Context, sessionID int64) ([]domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM item WHERE payment_session_id = @session_id
	           ORDER BY id`
	return r.queryItems(ctx, q, pgx.NamedArgs{"session_id": sessionID})
}

func (r *Repo) OrderItems(ctx context.Context, orderID int64) ([]domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM item WHERE order_id = @order_id ORDER BY id`
	return r.queryItems(ctx, q, pgx.NamedArgs{"order_id": orderID})
}

// UnpaidItems is the checkout-expiry sweep's list: live lines older than the checkout window
// that no order covers. Whether the money arrived after all is finance's answer, not this
// query's — the caller asks before it cancels anything.
func (r *Repo) UnpaidItems(ctx context.Context, before time.Time, limit int) ([]domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM item
	           WHERE order_id IS NULL AND cancelled_at IS NULL AND created_at < @before
	           ORDER BY created_at
	           LIMIT @limit`
	return r.queryItems(ctx, q, pgx.NamedArgs{"before": before, "limit": limit})
}

func (r *Repo) queryItems(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Item, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query items: %w", err)
	}
	defer rows.Close()

	var out []domain.Item
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate items: %w", err)
	}
	return out, nil
}

// SaveItem writes a cancellation. `order_id IS NULL` is the guard: a line already covered
// by an order is refunded, not cancelled.
func (r *Repo) SaveItem(ctx context.Context, i domain.Item) error {
	const q = `UPDATE item SET cancelled_at = @cancelled_at, cancelled_by_id = @cancelled_by
	           WHERE id = @id AND order_id IS NULL AND cancelled_at IS NULL`
	args := pgx.NamedArgs{
		"id": i.ID, "cancelled_at": i.CancelledAt, "cancelled_by": i.CancelledByID,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrItemNotCancellable
	}
	return nil
}

const transportColumns = `id, option, status::text, fee, data, created_at`

func (r *Repo) InsertTransport(ctx context.Context, option string, fee int64) (int64, error) {
	const q = `INSERT INTO transport (option, fee) VALUES (@option, @fee) RETURNING id`
	var id int64
	args := pgx.NamedArgs{"option": option, "fee": fee}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&id); err != nil {
		return 0, fmt.Errorf("db insert transport: %w", err)
	}
	return id, nil
}

func (r *Repo) FindTransport(ctx context.Context, id int64) (domain.Transport, error) {
	const q = `SELECT ` + transportColumns + ` FROM transport WHERE id = @id`
	var t domain.Transport
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}).
		Scan(&t.ID, &t.Option, &t.Status, &t.Fee, &t.Data, &t.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Transport{}, domain.ErrTransportNotFound
	}
	if err != nil {
		return domain.Transport{}, fmt.Errorf("db scan transport: %w", err)
	}
	return t, nil
}

// SaveTransport advances a shipment. `from` is the conditional write: a shipment carries no
// version, and two carrier reports landing at once must not both apply — the one that read a
// status somebody else has already moved on from loses.
func (r *Repo) SaveTransport(ctx context.Context, t domain.Transport, from string) error {
	const q = `UPDATE transport SET status = @status, data = @data
	           WHERE id = @id AND status::text = @from`
	args := pgx.NamedArgs{
		"id": t.ID, "status": t.Status, "from": from,
		"data": dbx.JSONObject(rawJSON(t.Data)),
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update transport: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTransportSettled
	}
	return nil
}

// BookTransport records what the courier gave back for a shipment it accepted: its own reference
// and whatever else it returned. Guarded on there being no booking yet, so a retried pass cannot
// replace the reference of a parcel the carrier has already taken.
func (r *Repo) BookTransport(ctx context.Context, transportID int64, data []byte) error {
	const q = `UPDATE transport SET data = @data
	           WHERE id = @id AND status = '` + domain.TransportPending + `'
	             AND data->>'provider_ref' IS NULL`
	args := pgx.NamedArgs{"id": transportID, "data": dbx.JSONObject(rawJSON(data))}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db book transport: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTransportSettled
	}
	return nil
}

// FindTransportByRef answers the shipment a carrier's own reference belongs to, which is all a
// webhook carries: a courier reports on its id, not on ours.
func (r *Repo) FindTransportByRef(ctx context.Context, ref string) (domain.Transport, error) {
	const q = `SELECT ` + transportColumns + ` FROM transport WHERE data->>'provider_ref' = @ref`
	var t domain.Transport
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"ref": ref}).
		Scan(&t.ID, &t.Option, &t.Status, &t.Fee, &t.Data, &t.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Transport{}, domain.ErrTransportNotFound
	}
	if err != nil {
		return domain.Transport{}, fmt.Errorf("db scan transport by ref: %w", err)
	}
	return t, nil
}

// UnbookedTransports is the retry list: shipments of live orders that no carrier has accepted
// yet. Bounded by age, so a booking that is merely a second old is left to the call that is
// already making it.
func (r *Repo) UnbookedTransports(ctx context.Context, before time.Time, limit int) ([]int64, error) {
	const q = `SELECT o.id FROM "order" o
	           JOIN transport t ON t.id = o.transport_id
	           WHERE t.status = '` + domain.TransportPending + `'
	             AND t.data->>'provider_ref' IS NULL
	             AND o.cancelled_at IS NULL AND o.completed_at IS NULL
	             AND t.created_at < @before
	           ORDER BY t.created_at
	           LIMIT @limit`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"before": before, "limit": limit})
	if err != nil {
		return nil, fmt.Errorf("db query unbooked transports: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db scan unbooked transport: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate unbooked transports: %w", err)
	}
	return out, nil
}

const orderColumns = `id, draft_id, offer_id, buyer_id, seller_id, transport_id, address,
	       pickup_address, received_at, receipt_attachments, payout_released_at, created_at,
	       completed_at, cancelled_at`

func scanOrder(row pgx.Row) (domain.Order, error) {
	var (
		o       domain.Order
		address []byte
		pickup  []byte
	)
	err := row.Scan(&o.ID, &o.DraftID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.TransportID,
		&address, &pickup, &o.ReceivedAt, &o.ReceiptAttachments, &o.PayoutReleasedAt,
		&o.CreatedAt, &o.CompletedAt, &o.CancelledAt)
	if dbx.IsNoRows(err) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("db scan order: %w", err)
	}
	if o.Address, err = domain.DecodeAddress(address); err != nil {
		return domain.Order{}, fmt.Errorf("decode order address: %w", err)
	}
	if o.PickupAddress, err = domain.DecodeAddress(pickup); err != nil {
		return domain.Order{}, fmt.Errorf("decode pickup address: %w", err)
	}
	return o, nil
}

// CreateOrder writes the order and links its lines in one transaction. The unique origin is
// what makes a redelivered webhook idempotent: the second attempt loses on the constraint
// rather than minting a second order.
func (r *Repo) CreateOrder(ctx context.Context, o *domain.Order, itemIDs []int64) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO "order" (draft_id, offer_id, buyer_id, seller_id, transport_id,
		                       address, pickup_address)
		           VALUES (@draft_id, @offer_id, @buyer_id, @seller_id, @transport_id,
		                   @address, @pickup_address)
		           RETURNING id, created_at`
		address, err := domain.EncodeAddress(o.Address)
		if err != nil {
			return fmt.Errorf("encode order address: %w", err)
		}
		pickup, err := domain.EncodeAddress(o.PickupAddress)
		if err != nil {
			return fmt.Errorf("encode pickup address: %w", err)
		}
		args := pgx.NamedArgs{
			"draft_id": o.DraftID, "offer_id": o.OfferID, "buyer_id": o.BuyerID,
			"seller_id": o.SellerID, "transport_id": o.TransportID,
			"address": address, "pickup_address": pickup,
		}
		if err := tx.QueryRow(ctx, q, args).Scan(&o.ID, &o.CreatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrOrderSettled
			}
			return fmt.Errorf("db insert order: %w", err)
		}
		linkArgs := pgx.NamedArgs{"order_id": o.ID, "ids": itemIDs}
		if _, err := tx.Exec(ctx, linkItems, linkArgs); err != nil {
			return fmt.Errorf("db link items: %w", err)
		}
		return nil
	})
}

// LinkItems attaches lines to an order that is already there — a settlement resumed after the
// order was written and something after it failed. Its own statement rather than a second
// CreateOrder, which would re-run the INSERT, lose on the origin constraint and roll back the
// link it was called to make.
func (r *Repo) LinkItems(ctx context.Context, orderID int64, itemIDs []int64) error {
	if len(itemIDs) == 0 {
		return nil
	}
	args := pgx.NamedArgs{"order_id": orderID, "ids": itemIDs}
	if _, err := r.pool.Exec(ctx, linkItems, args); err != nil {
		return fmt.Errorf("db link items: %w", err)
	}
	return nil
}

// linkItems only ever claims a line no order covers, so a line already linked — to this order or
// to another one — is left where it is.
const linkItems = `UPDATE item SET order_id = @order_id
                   WHERE id = ANY(@ids) AND order_id IS NULL AND cancelled_at IS NULL`

func (r *Repo) FindOrder(ctx context.Context, id int64) (domain.Order, error) {
	const q = `SELECT ` + orderColumns + ` FROM "order" WHERE id = @id`
	return scanOrder(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// FindOrderByOrigin answers "was this sale already turned into an order" — the question a
// redelivered webhook asks before it does anything.
func (r *Repo) FindOrderByOrigin(ctx context.Context, origin domain.Origin) (domain.Order, error) {
	const q = `SELECT ` + orderColumns + ` FROM "order"
	           WHERE (@draft_id::bigint IS NOT NULL AND draft_id = @draft_id::bigint)
	              OR (@offer_id::bigint IS NOT NULL AND offer_id = @offer_id::bigint)`
	args := pgx.NamedArgs{"draft_id": origin.DraftID, "offer_id": origin.OfferID}
	return scanOrder(r.pool.QueryRow(ctx, q, args))
}

func (r *Repo) ListOrders(ctx context.Context, f port.OrderFilter) ([]domain.Order, error) {
	// The state is derived from the two outcome timestamps, so it is a predicate rather
	// than a column — and 'open' is exactly the partial indexes' own predicate.
	const q = `SELECT ` + orderColumns + ` FROM "order"
	           WHERE (@buyer_id = 0 OR buyer_id = @buyer_id)
	             AND (@seller_id = 0 OR seller_id = @seller_id)
	             AND (@state::text IS NULL
	                  OR (@state::text = '` + domain.StateOpen + `'
	                      AND completed_at IS NULL AND cancelled_at IS NULL)
	                  OR (@state::text = '` + domain.StateCompleted + `' AND completed_at IS NOT NULL)
	                  OR (@state::text = '` + domain.StateCancelled + `' AND cancelled_at IS NOT NULL))
	             AND (@before::timestamptz IS NULL
	                  OR (created_at, id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"buyer_id": f.BuyerID, "seller_id": f.SellerID, "state": dbx.NullText(f.State),
		"before": before, "before_id": beforeID, "limit": limit,
	}
	return r.queryOrders(ctx, q, args)
}

// PayoutDue is the escrow release *candidate* list: a confirmed receipt whose window has passed,
// with no refund on the order that could still claim the money. Candidates only — the guard is
// re-read under the order's lock by ClaimPayout, because a `NOT EXISTS` here is a read and a
// refund committed a moment later would be invisible to it.
//
// 'rejected' and 'cancelled' are the two that do not block: the seller won, or the buyer walked
// away. Every other status — including 'accepted', which means the buyer has been paid — leaves
// the escrow somebody else's.
func (r *Repo) PayoutDue(ctx context.Context, now time.Time, limit int) ([]domain.Order, error) {
	const q = `SELECT ` + orderColumns + ` FROM "order" o
	           WHERE o.completed_at IS NULL AND o.cancelled_at IS NULL
	             AND o.received_at IS NOT NULL
	             AND o.received_at + @window::interval < @now
	             AND NOT EXISTS (` + liveRefund + `)
	           ORDER BY o.received_at
	           LIMIT @limit`
	args := pgx.NamedArgs{
		"now": now, "limit": limit,
		"window": fmt.Sprintf("%d hours", int(domain.PayoutWindow.Hours())),
	}
	return r.queryOrders(ctx, q, args)
}

// liveRefund is any refund that still has a claim on the order's escrow. Correlated on `o`, so
// both the candidate list and the locked re-check ask exactly the same question.
const liveRefund = `SELECT 1 FROM refund
                    WHERE refund.order_id = o.id
                      AND refund.status NOT IN ('` + domain.RefundRejected + `',
	                                               '` + domain.RefundCancelled + `')`

// orderEscrowLock namespaces the advisory lock this module takes on an order. A constant first
// key, so it cannot collide with another module's lock on the same id.
const orderEscrowLock = 0x4f524445 // 'ORDE'

// ClaimPayout is the payout's decision, made under the order's advisory lock: the same
// live-refund question PayoutDue asked is re-read, and the order is completed in the same
// transaction. That serialises it against CreateRefund, which takes the same lock — without it
// each statement sees a world in which its own write is legal (write skew), the sweep releases
// the escrow to the seller and the verdict then refunds the buyer out of a hold that is gone.
//
// The claim is what makes the release safe to retry: whoever writes the order's outcome owns
// the escrow, so a second pass finds nothing to pay.
func (r *Repo) ClaimPayout(ctx context.Context, o *domain.Order) error {
	completedAt := time.Now()
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const lock = `SELECT pg_advisory_xact_lock(@space, @order_id)`
		lockArgs := pgx.NamedArgs{"space": int64(orderEscrowLock), "order_id": o.ID}
		if _, err := tx.Exec(ctx, lock, lockArgs); err != nil {
			return fmt.Errorf("db lock order: %w", err)
		}
		const claim = `UPDATE "order" o SET completed_at = @completed_at
		               WHERE o.id = @id AND o.completed_at IS NULL AND o.cancelled_at IS NULL
		                 AND o.received_at IS NOT NULL
		                 AND NOT EXISTS (` + liveRefund + `)`
		args := pgx.NamedArgs{"id": o.ID, "completed_at": completedAt}
		tag, err := tx.Exec(ctx, claim, args)
		if err != nil {
			return fmt.Errorf("db claim payout: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrOrderSettled
		}
		return nil
	})
	if err != nil {
		return err
	}
	o.CompletedAt = &completedAt
	return nil
}

// MarkPayoutReleased records that the money reached the seller, which is what takes the order
// off the stranded list. Guarded by the column being NULL, so a repeat is a no-op rather than a
// moved timestamp.
func (r *Repo) MarkPayoutReleased(ctx context.Context, o domain.Order) error {
	const q = `UPDATE "order" SET payout_released_at = @released_at
	           WHERE id = @id AND payout_released_at IS NULL`
	args := pgx.NamedArgs{"id": o.ID, "released_at": o.PayoutReleasedAt}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db mark payout released: %w", err)
	}
	return nil
}

// ClaimedPayouts is the stranded releases: an order whose outcome was written but whose money
// never moved. The claim stopped every other pass from touching them, so this is the only list
// that would try again — and `payout_released_at IS NULL` is what makes it *exactly* that list,
// so a healthy platform reads nothing here rather than re-asking finance about every sale it has
// ever completed. Oldest first, and no time bound: money owed does not stop being owed.
func (r *Repo) ClaimedPayouts(ctx context.Context, limit int) ([]domain.Order, error) {
	const q = `SELECT ` + orderColumns + ` FROM "order" o
	           WHERE o.completed_at IS NOT NULL AND o.payout_released_at IS NULL
	             AND o.cancelled_at IS NULL
	           ORDER BY o.completed_at
	           LIMIT @limit`
	return r.queryOrders(ctx, q, pgx.NamedArgs{"limit": limit})
}

func (r *Repo) queryOrders(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Order, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query orders: %w", err)
	}
	defer rows.Close()

	var out []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate orders: %w", err)
	}
	return out, nil
}

// SaveOrder writes the receipt and the outcome. Guarded on the order still being open, so
// two payouts or a payout racing a cancellation cannot both land.
func (r *Repo) SaveOrder(ctx context.Context, o domain.Order) error {
	tag, err := r.pool.Exec(ctx, saveOrder, orderArgs(o))
	if err != nil {
		return fmt.Errorf("db update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderSettled
	}
	return nil
}

const saveOrder = `UPDATE "order"
                   SET received_at = @received_at,
                       receipt_attachments = @receipt_attachments,
                       payout_released_at = @payout_released_at,
                       completed_at = @completed_at, cancelled_at = @cancelled_at
                   WHERE id = @id AND completed_at IS NULL AND cancelled_at IS NULL`

func orderArgs(o domain.Order) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id": o.ID, "received_at": o.ReceivedAt,
		"receipt_attachments": dbx.Int64Array(o.ReceiptAttachments),
		"payout_released_at":  o.PayoutReleasedAt,
		"completed_at":        o.CompletedAt, "cancelled_at": o.CancelledAt,
	}
}

func rawJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

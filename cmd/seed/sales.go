package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
)

const (
	// transportOption is the carrier slug every seeded shipment names. It is a plain string on
	// the row with no foreign key — past orders hold it that way on purpose — so a shipment
	// resolves through its own history even though migration 003 removed the registry row that
	// once backed it. Booking a *new* shipment needs a live "option" row of type 'transport',
	// which is the operator's to create and emphatically not this command's.
	transportOption = "standard-delivery"
	// receiptPhoto stands in for the unboxing evidence.
	// "order_receipt_attachments_match_received" makes a confirmed receipt with an empty
	// gallery an illegal row, so a received order has to point at something.
	receiptPhoto = "seed/evidence/receipt.png"
	// refundPhoto is the buyer's evidence on a refund request. Its own object rather than the
	// receipt's, because the two are different claims and a moderator reading a dispute is
	// looking at what the buyer *showed*.
	refundPhoto = "seed/evidence/refund-evidence.png"
	// reviewPhoto is the photo a buyer attaches to a review. Reviews with one are what a
	// product page leads with, so a demo where none has one shows the plainest possible
	// version of that block.
	reviewPhoto = "seed/evidence/review-photo.png"
)

// salesResult is what the later steps need back.
type salesResult struct {
	orderIDs map[string]int64
	offerIDs map[string]int64
	// totals is the goods-only amount of each order, which is what the escrow held and what
	// the release paid out. The delivery fee is the carrier's and is not in it.
	totals map[string]int64
	fees   map[string]int64
}

// writeSales lands every purchase the plan invented: the negotiation or the spent draft it came
// from, the shipment, the order, its one line, and the refund if there is one.
//
// One transaction, because the counters in catalog were computed against exactly this set: a
// partial write would leave a listing claiming sales that are not there.
func writeSales(
	ctx context.Context, pool *pgxpool.Pool, p *plan,
	parties map[string]party, cat catalogIDs, sessions map[string]int64,
) (salesResult, error) {
	out := salesResult{
		orderIDs: map[string]int64{},
		offerIDs: map[string]int64{},
		totals:   map[string]int64{},
		fees:     map[string]int64{},
	}
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		receiptID, err := evidenceResource(ctx, tx, receiptPhoto, p.evidenceSize(receiptPhoto), p.now)
		if err != nil {
			return err
		}
		refundEvidenceID, err := evidenceResource(ctx, tx, refundPhoto, p.evidenceSize(refundPhoto), p.now)
		if err != nil {
			return err
		}

		// Offers first: an order born of one names it, and an offer names nothing.
		for _, o := range p.offers {
			id, err := writeOffer(ctx, tx, o, parties, cat)
			if err != nil {
				return err
			}
			out.offerIDs[o.key] = id
		}

		for _, o := range p.orders {
			buyer, ok := parties[o.buyer]
			if !ok {
				return fmt.Errorf("order %s: no such buyer %q", o.key, o.buyer)
			}
			seller, ok := parties[o.seller]
			if !ok {
				return fmt.Errorf("order %s: no such seller %q", o.key, o.seller)
			}
			l := p.listings[o.listing]
			total := l.variants[o.variant].price * o.quantity
			if o.offerKey != "" {
				of, ok := p.offer(o.offerKey)
				if !ok {
					return fmt.Errorf("order %s: no such offer %q", o.key, o.offerKey)
				}
				total = of.total
			}
			fee := deliveryFee(buyer.area.provinceCode == seller.area.provinceCode)

			id, err := writeOrder(ctx, tx, orderRow{
				plan:       o,
				listing:    l,
				listingID:  cat.listings[o.listing],
				variantID:  cat.variants[o.listing][o.variant],
				variantIDs: cat.variants[o.listing],
				offerID:    out.offerIDs[o.offerKey],
				sessionID:  sessions[o.key],
				buyer:      buyer,
				seller:     seller,
				total:      total,
				fee:        fee,
				receiptID:  receiptID,
				evidenceID: refundEvidenceID,
				now:        p.now,
			})
			if err != nil {
				return fmt.Errorf("order %s: %w", o.key, err)
			}
			out.orderIDs[o.key] = id
			out.totals[o.key] = total
			out.fees[o.key] = fee
		}
		return nil
	})
	if err != nil {
		return salesResult{}, err
	}
	return out, nil
}

func writeOffer(ctx context.Context, tx pgx.Tx, o offerPlan, parties map[string]party, cat catalogIDs) (int64, error) {
	buyer, ok := parties[o.buyer]
	if !ok {
		return 0, fmt.Errorf("offer %s: no such buyer %q", o.key, o.buyer)
	}
	seller, ok := parties[o.seller]
	if !ok {
		return 0, fmt.Errorf("offer %s: no such seller %q", o.key, o.seller)
	}
	author, ok := parties[o.author]
	if !ok {
		return 0, fmt.Errorf("offer %s: no such author %q", o.key, o.author)
	}
	const q = `
		INSERT INTO offer (listing_id, variant_id, author_id, buyer_id, seller_id, status,
		                   quantity, total, reason, created_at, expires_at)
		VALUES (@listing_id, @variant_id, @author_id, @buyer_id, @seller_id, @status,
		        @quantity, @total, @reason, @created_at, @expires_at)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"listing_id": cat.listings[o.listing],
		"variant_id": cat.variants[o.listing][o.variant],
		"author_id":  author.id,
		"buyer_id":   buyer.id,
		"seller_id":  seller.id,
		"status":     o.status,
		"quantity":   o.quantity,
		"total":      o.total,
		"reason":     o.reason,
		"created_at": o.createdAt,
		"expires_at": o.expiresAt,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert offer %s: %w", o.key, err)
	}
	return id, nil
}

type orderRow struct {
	plan       orderPlan
	listing    listingPlan
	listingID  int64
	variantID  int64
	variantIDs []int64
	offerID    int64
	sessionID  int64
	buyer      party
	seller     party
	total      int64
	fee        int64
	receiptID  int64
	evidenceID int64
	now        time.Time
}

// timeline turns the state into the timestamps the schema derives it from. There is no status
// column on "order" — "who is waiting on whom" is read off these — so this function is the
// definition of every state the report photographs.
type timeline struct {
	confirmedAt *time.Time
	receivedAt  *time.Time
	payoutAt    *time.Time
	completedAt *time.Time
	cancelledAt *time.Time
	transport   string
}

func timelineFor(o orderPlan) timeline {
	at := func(d time.Duration) *time.Time { t := o.createdAt.Add(d); return &t }
	switch o.state {
	case stateAwaitingConfirmation:
		return timeline{transport: "pending"}
	case statePreparing:
		return timeline{confirmedAt: at(2 * time.Hour), transport: "pending"}
	case stateInTransit:
		return timeline{confirmedAt: at(3 * time.Hour), transport: "in-transit"}
	case stateDelivered:
		return timeline{confirmedAt: at(4 * time.Hour), transport: "delivered"}
	case stateDeclined:
		return timeline{cancelledAt: at(8 * time.Hour), transport: "pending"}
	case stateRefundRequested, stateRefundDisputed:
		return timeline{
			confirmedAt: at(3 * time.Hour),
			receivedAt:  at(4 * 24 * time.Hour),
			transport:   "delivered",
		}
	case stateRefundAccepted:
		// A settled refund cancels the order, and the escrow goes back to the buyer rather
		// than out to the seller — so there is a receipt but no payout.
		return timeline{
			confirmedAt: at(3 * time.Hour),
			receivedAt:  at(3 * 24 * time.Hour),
			cancelledAt: at(21 * 24 * time.Hour),
			transport:   "delivered",
		}
	default: // stateCompleted
		received := o.createdAt.Add(3 * 24 * time.Hour)
		payout := received.Add(escrowWindow)
		return timeline{
			confirmedAt: at(3 * time.Hour),
			receivedAt:  &received,
			payoutAt:    &payout,
			completedAt: &payout,
			transport:   "delivered",
		}
	}
}

func writeOrder(ctx context.Context, tx pgx.Tx, r orderRow) (int64, error) {
	tl := timelineFor(r.plan)

	transportID, err := writeTransport(ctx, tx, tl.transport, r.fee, r.plan.key, r.plan.createdAt)
	if err != nil {
		return 0, err
	}

	// A fixed-price sale is checked out from a draft; a negotiated one has none, because the
	// accepted offer is what froze its terms. Exactly one of the two, which is the schema's
	// own "order_origin_exactly_one".
	var draftID any
	var offerID any
	if r.offerID != 0 {
		offerID = r.offerID
	} else {
		id, err := writeDraft(ctx, tx, r)
		if err != nil {
			return 0, err
		}
		draftID = id
	}

	receipts := []int64{}
	if tl.receivedAt != nil {
		receipts = append(receipts, r.receiptID)
	}
	const insertOrder = `
		INSERT INTO "order" (draft_id, offer_id, buyer_id, transport_id, address, pickup_address,
		                     confirmed_at, decline_reason,
		                     received_at, receipt_attachments, payout_released_at,
		                     seller_id, created_at, completed_at, cancelled_at)
		VALUES (@draft_id, @offer_id, @buyer_id, @transport_id, @address, @pickup_address,
		        @confirmed_at, @decline_reason,
		        @received_at, @receipt_attachments, @payout_released_at,
		        @seller_id, @created_at, @completed_at, @cancelled_at)
		RETURNING id`
	var orderID int64
	err = tx.QueryRow(ctx, insertOrder, pgx.NamedArgs{
		"draft_id":            draftID,
		"offer_id":            offerID,
		"buyer_id":            r.buyer.id,
		"transport_id":        transportID,
		"address":             r.buyer.address,
		"pickup_address":      r.seller.address,
		"confirmed_at":        tl.confirmedAt,
		"decline_reason":      dbx.NullText(r.plan.declineReason),
		"received_at":         tl.receivedAt,
		"receipt_attachments": receipts,
		"payout_released_at":  tl.payoutAt,
		"seller_id":           r.seller.id,
		"created_at":          r.plan.createdAt,
		"completed_at":        tl.completedAt,
		"cancelled_at":        tl.cancelledAt,
	}).Scan(&orderID)
	if err != nil {
		return 0, fmt.Errorf("insert order: %w", err)
	}

	const insertItem = `
		INSERT INTO item (draft_id, offer_id, order_id, buyer_id, seller_id, listing_id, variant_id,
		                  address, note, currency, quantity, transport_option, total_amount,
		                  payment_session_id, created_at)
		VALUES (@draft_id, @offer_id, @order_id, @buyer_id, @seller_id, @listing_id, @variant_id,
		        @address, @note, @currency, @quantity, @transport_option, @total_amount,
		        @payment_session_id, @created_at)`
	_, err = tx.Exec(ctx, insertItem, pgx.NamedArgs{
		"draft_id":         draftID,
		"offer_id":         offerID,
		"order_id":         orderID,
		"buyer_id":         r.buyer.id,
		"seller_id":        r.seller.id,
		"listing_id":       r.listingID,
		"variant_id":       r.variantID,
		"address":          r.buyer.address,
		"note":             dbx.NullText(r.plan.note),
		"currency":         currency,
		"quantity":         r.plan.quantity,
		"transport_option": transportOption,
		// Goods only. The delivery fee is the carrier's and sits on "transport"."fee", which
		// is what keeps a payout from handing it to the seller.
		"total_amount":       r.total,
		"payment_session_id": r.sessionID,
		"created_at":         r.plan.createdAt,
	})
	if err != nil {
		return 0, fmt.Errorf("insert item: %w", err)
	}

	if r.plan.refund != nil {
		if err := writeRefund(ctx, tx, r, orderID); err != nil {
			return 0, err
		}
	}
	return orderID, nil
}

func writeTransport(ctx context.Context, tx pgx.Tx, status string, fee int64, ref string, at time.Time) (int64, error) {
	const q = `
		INSERT INTO transport (option, status, fee, data, created_at)
		VALUES (@option, @status, @fee, @data, @created_at)
		RETURNING id`
	var id int64
	// provider_ref is not decoration: the retry sweep looks for pending shipments that have
	// none and books them with a real courier. A seeded shipment must not be in that list.
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"option":     transportOption,
		"status":     status,
		"fee":        fee,
		"data":       map[string]any{"provider_ref": "SEED-" + ref},
		"created_at": at,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert transport: %w", err)
	}
	return id, nil
}

func writeDraft(ctx context.Context, tx pgx.Tx, r orderRow) (int64, error) {
	// The draft is spent, not live: it was claimed at checkout, which is what stops a second
	// press of the button opening a second session on the same sale.
	const q = `
		INSERT INTO draft_order (buyer_id, listing_id, spu_snapshot, created_at,
		                         cancelled_at, valid_until)
		VALUES (@buyer_id, @listing_id, @snapshot, @created_at, @created_at, @valid_until)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"buyer_id":    r.buyer.id,
		"listing_id":  r.listingID,
		"snapshot":    listingSnapshot(r),
		"created_at":  r.plan.createdAt,
		"valid_until": r.plan.createdAt.Add(time.Hour),
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert draft: %w", err)
	}
	return id, nil
}

// listingSnapshot is the listing as the draft froze it — domain.ListingSnapshot's JSON, whose
// field names are a stored contract.
func listingSnapshot(r orderRow) map[string]any {
	variants := make([]map[string]any, 0, len(r.variantIDs))
	for i, v := range r.listing.variants {
		variants = append(variants, map[string]any{
			"variant_id":      r.variantIDs[i],
			"price":           v.price,
			"attributes":      v.attributes,
			"package_details": v.pkg,
		})
	}
	return map[string]any{
		"listing_id": r.listingID,
		"seller_id":  r.seller.id,
		"name":       r.listing.name,
		"currency":   currency,
		"price_mode": r.listing.priceMode,
		"variants":   variants,
	}
}

// sellerReviewWindow mirrors the refund service's: how long the seller has to answer before
// the case is handed to staff for them.
const sellerReviewWindow = 48 * time.Hour

func writeRefund(ctx context.Context, tx pgx.Tx, r orderRow, orderID int64) error {
	rf := r.plan.refund

	// "refund_deadline_matches_status": a deadline exists in exactly the two states where a
	// party is on the clock, and nowhere else. A deadline in the *past* is worse than a wrong
	// one — the overdue sweep would advance the row the moment the gateway came up, and the
	// screen the report wants a photograph of would be gone.
	var deadline any
	if rf.status == "awaiting-seller-review" || rf.status == "returned" {
		d := rf.createdAt.Add(sellerReviewWindow)
		if !d.After(r.now) {
			d = r.now.Add(sellerReviewWindow / 2)
		}
		deadline = d
	}
	var sellerDecidedAt any
	var returnTransportID any
	var returnedAt any
	switch rf.status {
	case "disputed":
		if !rf.escalated {
			// Handed to staff because the seller let the window pass, so they never answered.
			break
		}
		t := rf.createdAt.Add(20 * time.Hour)
		sellerDecidedAt = t
	case "accepted":
		decided := rf.createdAt.Add(18 * time.Hour)
		sellerDecidedAt = decided
		// The return leg exists only from the moment the refund is granted, and it is its own
		// shipment: "refund_return_transport_id_key" is unique and the order already has one.
		id, err := writeTransport(ctx, tx, "delivered", 0, r.plan.key+"-return", decided)
		if err != nil {
			return err
		}
		returnTransportID = id
		returnedAt = decided.Add(4 * 24 * time.Hour)
	}

	const q = `
		INSERT INTO refund (buyer_id, order_id, reason, attachments, created_at,
		                    status, deadline_at, seller_decided_at,
		                    return_transport_id, returned_at)
		VALUES (@buyer_id, @order_id, @reason, @attachments, @created_at,
		        @status, @deadline_at, @seller_decided_at,
		        @return_transport_id, @returned_at)`
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"buyer_id":            r.buyer.id,
		"order_id":            orderID,
		"reason":              rf.reason,
		"attachments":         []int64{r.evidenceID},
		"created_at":          rf.createdAt,
		"status":              rf.status,
		"deadline_at":         deadline,
		"seller_decided_at":   sellerDecidedAt,
		"return_transport_id": returnTransportID,
		"returned_at":         returnedAt,
	})
	if err != nil {
		return fmt.Errorf("insert refund: %w", err)
	}
	return nil
}

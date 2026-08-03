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
	// transportOption is the carrier row order's own migration seeds, so this is the one
	// slug a seeded order can name and still resolve.
	transportOption = "standard-delivery"
	// escrowWindow is how long after a confirmed receipt the money reaches the seller.
	// Mirrors the service's own window; a seeded order is past it, so every one of them is
	// paid out.
	escrowWindow = 72 * time.Hour
	// receiptPhoto stands in for the unboxing evidence. "order_receipt_attachments_match_received"
	// makes a confirmed receipt with an empty gallery an illegal row, so a completed order
	// has to point at something.
	receiptPhoto = "seed/receipt-placeholder.jpg"
)

// writeSales lands every completed purchase the plan invented: the spent draft it was
// checked out from, the delivered parcel, the order and its one line.
//
// No finance rows. A seeded order has no payment session, no escrow movement and no wallet
// entry — "item"."payment_session_id" is 0, which is not an id anybody holds. Everything
// downstream of the money (the order, the delivery, the review, the reputation) is
// consistent; the ledger that would have paid for it is empty.
func writeSales(ctx context.Context, pool *pgxpool.Pool, p *plan, sellers []seller, cat catalogIDs) ([][]int64, error) {
	orders := make([][]int64, len(p.listings))
	now := time.Now()

	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		receiptID, err := writeReceiptResource(ctx, tx, now)
		if err != nil {
			return err
		}
		for i, l := range p.listings {
			if len(l.sales) == 0 {
				continue
			}
			orders[i] = make([]int64, len(l.sales))
			snapshot := listingSnapshot(l, sellers[l.seller].id, cat.listings[i], cat.variants[i])
			for j, s := range l.sales {
				id, err := writeSale(ctx, tx, saleRow{
					sale:       s,
					listing:    l,
					listingID:  cat.listings[i],
					variantID:  cat.variants[i][s.variant],
					snapshot:   snapshot,
					buyer:      sellers[s.buyer],
					seller:     sellers[l.seller],
					receiptID:  receiptID,
					receivedAt: s.orderedAt.Add(s.reviewedAt.Sub(s.orderedAt) / 2),
					now:        now,
				})
				if err != nil {
					return fmt.Errorf("write sale of %q: %w", l.slug, err)
				}
				orders[i][j] = id
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return orders, nil
}

type saleRow struct {
	sale       salePlan
	listing    listingPlan
	listingID  int64
	variantID  int64
	snapshot   map[string]any
	buyer      seller
	seller     seller
	receiptID  int64
	receivedAt time.Time
	now        time.Time
}

func writeSale(ctx context.Context, tx pgx.Tx, r saleRow) (int64, error) {
	// The draft is spent, not live: it was claimed at checkout, which is what stops a
	// second press of the button opening a second session on the same sale.
	const insertDraft = `
		INSERT INTO draft_order (buyer_id, listing_id, spu_snapshot, created_at,
		                         cancelled_at, valid_until)
		VALUES (@buyer_id, @listing_id, @snapshot, @created_at,
		        @created_at, @valid_until)
		RETURNING id`
	var draftID int64
	err := tx.QueryRow(ctx, insertDraft, pgx.NamedArgs{
		"buyer_id":    r.buyer.id,
		"listing_id":  r.listingID,
		"snapshot":    r.snapshot,
		"created_at":  r.sale.orderedAt,
		"valid_until": r.sale.orderedAt.Add(time.Hour),
	}).Scan(&draftID)
	if err != nil {
		return 0, fmt.Errorf("insert draft: %w", err)
	}

	const insertTransport = `
		INSERT INTO transport (option, status, fee, data, created_at)
		VALUES (@option, 'delivered', @fee, @data, @created_at)
		RETURNING id`
	var transportID int64
	err = tx.QueryRow(ctx, insertTransport, pgx.NamedArgs{
		"option":     transportOption,
		"fee":        r.sale.fee,
		"data":       map[string]any{"provider_ref": fmt.Sprintf("SEED-%d", draftID)},
		"created_at": r.sale.orderedAt,
	}).Scan(&transportID)
	if err != nil {
		return 0, fmt.Errorf("insert transport: %w", err)
	}

	payoutAt := minTime(r.receivedAt.Add(escrowWindow), r.now)
	const insertOrder = `
		INSERT INTO "order" (draft_id, buyer_id, transport_id, address, pickup_address,
		                     received_at, receipt_attachments, payout_released_at,
		                     seller_id, created_at, completed_at)
		VALUES (@draft_id, @buyer_id, @transport_id, @address, @pickup_address,
		        @received_at, @receipt_attachments, @payout_released_at,
		        @seller_id, @created_at, @completed_at)
		RETURNING id`
	var orderID int64
	err = tx.QueryRow(ctx, insertOrder, pgx.NamedArgs{
		"draft_id":            draftID,
		"buyer_id":            r.buyer.id,
		"transport_id":        transportID,
		"address":             r.buyer.address,
		"pickup_address":      r.seller.address,
		"received_at":         r.receivedAt,
		"receipt_attachments": []int64{r.receiptID},
		"payout_released_at":  payoutAt,
		"seller_id":           r.seller.id,
		"created_at":          r.sale.orderedAt,
		"completed_at":        payoutAt,
	}).Scan(&orderID)
	if err != nil {
		return 0, fmt.Errorf("insert order: %w", err)
	}

	const insertItem = `
		INSERT INTO item (draft_id, order_id, buyer_id, seller_id, listing_id, variant_id,
		                  address, currency, quantity, transport_option, total_amount,
		                  payment_session_id, created_at)
		VALUES (@draft_id, @order_id, @buyer_id, @seller_id, @listing_id, @variant_id,
		        @address, @currency, @quantity, @transport_option, @total_amount,
		        0, @created_at)`
	_, err = tx.Exec(ctx, insertItem, pgx.NamedArgs{
		"draft_id":         draftID,
		"order_id":         orderID,
		"buyer_id":         r.buyer.id,
		"seller_id":        r.seller.id,
		"listing_id":       r.listingID,
		"variant_id":       r.variantID,
		"address":          r.buyer.address,
		"currency":         r.listing.currency,
		"quantity":         r.sale.quantity,
		"transport_option": transportOption,
		// Goods only. The delivery fee is the carrier's and sits on "transport"."fee",
		// which is what keeps a payout from handing it to the seller.
		"total_amount": r.listing.variants[r.sale.variant].price * r.sale.quantity,
		"created_at":   r.sale.orderedAt,
	})
	if err != nil {
		return 0, fmt.Errorf("insert item: %w", err)
	}
	return orderID, nil
}

// listingSnapshot is the listing as the draft froze it — domain.ListingSnapshot's JSON,
// whose field names are a stored contract.
func listingSnapshot(l listingPlan, sellerID, listingID int64, variantIDs []int64) map[string]any {
	variants := make([]map[string]any, 0, len(l.variants))
	for i, v := range l.variants {
		variants = append(variants, map[string]any{
			"variant_id":      variantIDs[i],
			"price":           v.price,
			"attributes":      v.attributes,
			"package_details": v.pkg,
		})
	}
	return map[string]any{
		"listing_id": listingID,
		"seller_id":  sellerID,
		"name":       l.name,
		"currency":   l.currency,
		"price_mode": "fixed",
		"variants":   variants,
	}
}

func writeReceiptResource(ctx context.Context, tx pgx.Tx, now time.Time) (int64, error) {
	const q = `
		INSERT INTO resource (uploaded_by_id, provider, object_key, mime, size,
		                      metadata, created_at, completed_at)
		VALUES (NULL, 'remote', @object_key, 'image/jpeg', 0, '{}', @now, @now)
		RETURNING id`
	var id int64
	args := pgx.NamedArgs{"object_key": receiptPhoto, "now": now}
	if err := tx.QueryRow(ctx, q, args).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert receipt resource: %w", err)
	}
	return id, nil
}

// writeTrust records what those sales earned: the buyer's review of the goods, and the
// reputation the reviews and the completed orders add up to.
//
// No "feedback" rows. Transaction feedback is blind and two-sided, and inventing both
// halves would make every seeded account look like it always rates back — a claim the rest
// of the data does not support.
func writeTrust(ctx context.Context, pool *pgxpool.Pool, p *plan, sellers []seller, cat catalogIDs, orders [][]int64) error {
	type tally struct{ ratingSum, ratingCount, asSeller, asBuyer int64 }
	tallies := make([]tally, len(sellers))
	now := time.Now()

	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		reviews := &pgx.Batch{}
		outcomes := &pgx.Batch{}
		for i, l := range p.listings {
			for j, s := range l.sales {
				reviews.Queue(
					`INSERT INTO review (listing_id, order_id, author_id, seller_id,
					                     rating, body, created_at)
					 VALUES (@listing_id, @order_id, @author_id, @seller_id,
					         @rating, @body, @created_at)`,
					pgx.NamedArgs{
						"listing_id": cat.listings[i],
						"order_id":   orders[i][j],
						"author_id":  sellers[s.buyer].id,
						"seller_id":  sellers[l.seller].id,
						"rating":     s.rating,
						"body":       s.body,
						"created_at": s.reviewedAt,
					})
				outcomes.Queue(
					`INSERT INTO order_outcome (order_id, completed, recorded_at)
					 VALUES (@order_id, true, @recorded_at)`,
					pgx.NamedArgs{"order_id": orders[i][j], "recorded_at": s.reviewedAt})

				tallies[l.seller].ratingSum += int64(s.rating)
				tallies[l.seller].ratingCount++
				tallies[l.seller].asSeller++
				tallies[s.buyer].asBuyer++
			}
		}
		if err := tx.SendBatch(ctx, reviews).Close(); err != nil {
			return fmt.Errorf("insert reviews: %w", err)
		}
		if err := tx.SendBatch(ctx, outcomes).Close(); err != nil {
			return fmt.Errorf("insert order outcomes: %w", err)
		}

		const insertReputation = `
			INSERT INTO reputation (account_id, role, review_rating_sum, review_rating_count,
			                        completed_orders, updated_at)
			VALUES (@account_id, @role, @review_rating_sum, @review_rating_count,
			        @completed_orders, @now)`
		for i, t := range tallies {
			// Two rows per account, because every seeded account both sells and buys.
			// "reputation_reviews_are_seller_only" is why the review halves are on one of
			// them only: nobody reviews a buyer's products.
			args := pgx.NamedArgs{
				"account_id":          sellers[i].id,
				"role":                "seller",
				"review_rating_sum":   t.ratingSum,
				"review_rating_count": t.ratingCount,
				"completed_orders":    t.asSeller,
				"now":                 now,
			}
			if _, err := tx.Exec(ctx, insertReputation, args); err != nil {
				return fmt.Errorf("insert seller reputation: %w", err)
			}
			args["role"] = "buyer"
			args["review_rating_sum"] = 0
			args["review_rating_count"] = 0
			args["completed_orders"] = t.asBuyer
			if _, err := tx.Exec(ctx, insertReputation, args); err != nil {
				return fmt.Errorf("insert buyer reputation: %w", err)
			}
		}
		return nil
	})
}

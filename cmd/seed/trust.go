package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
)

// What the trade earned: the buyer's review of the goods, the seller answering it, the votes
// that decide which review a product page shows first, the two-way transaction feedback, and
// the reputation all of that adds up to.
//
// The old seeder wrote reviews and left "feedback" out, on the grounds that inventing both
// halves of a blind two-way rating overstates how often people rate back. The result was a
// reputation row whose "rating_count" was zero on every account — and the shop header reads
// that number, not the review one, so every seller showed 0.0 ★. Feedback is written here, on
// a fraction of orders rather than all of them, which is the honest version of the same point.

// writeTrust returns the ids of the tickets it wrote, because chat opens a thread per ticket
// and the ticket then has to point back at it.
func writeTrust(
	ctx context.Context, pool *pgxpool.Pool, p *plan,
	parties map[string]party, cat catalogIDs, sales salesResult,
) (map[string]int64, error) {
	rng := rand.New(rand.NewPCG(planSeed, planSeed+1))
	ticketIDs := map[string]int64{}

	// One aggregate per account per role, built as the rows are written so the counters and
	// the rows behind them cannot drift.
	type tally struct {
		reviewSum, reviewCount             int64
		sellerFeedbackSum, sellerFeedback  int64
		buyerFeedbackSum, buyerFeedback    int64
		completedAsSeller, cancelledSeller int64
		completedAsBuyer, cancelledBuyer   int64
	}
	tallies := map[string]*tally{}
	get := func(key string) *tally {
		t := tallies[key]
		if t == nil {
			t = &tally{}
			tallies[key] = t
		}
		return t
	}

	voters := make([]string, 0, len(seedAccounts))
	for _, a := range seedAccounts {
		if a.Key != adminKey {
			voters = append(voters, a.Key)
		}
	}

	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		photoID, err := evidenceResource(ctx, tx, reviewPhoto, p.evidenceSize(reviewPhoto), p.now)
		if err != nil {
			return err
		}

		for _, o := range p.orders {
			orderID := sales.orderIDs[o.key]
			settled := o.state == stateCompleted
			cancelled := o.state == stateDeclined || o.state == stateRefundAccepted

			if settled || cancelled {
				// The at-least-once bus can deliver a settled event twice, and this key is
				// what makes the second delivery a no-op instead of a second increment.
				const outcome = `
					INSERT INTO order_outcome (order_id, completed, recorded_at)
					VALUES (@order_id, @completed, @recorded_at)
					ON CONFLICT (order_id) DO NOTHING`
				if _, err := tx.Exec(ctx, outcome, pgx.NamedArgs{
					"order_id": orderID, "completed": settled, "recorded_at": o.createdAt.Add(7 * 24 * time.Hour),
				}); err != nil {
					return fmt.Errorf("insert order outcome for %s: %w", o.key, err)
				}
				if settled {
					get(o.seller).completedAsSeller++
					get(o.buyer).completedAsBuyer++
				} else {
					get(o.seller).cancelledSeller++
					get(o.buyer).cancelledBuyer++
				}
			}

			if o.review != nil {
				reviewID, err := writeReview(ctx, tx, reviewRow{
					plan:      *o.review,
					listingID: cat.listings[o.listing],
					orderID:   orderID,
					author:    parties[o.buyer].id,
					seller:    parties[o.seller].id,
					// A photo of what arrived, on some of them.
					attachment: photoID,
					withPhoto:  o.review.rating >= 4 && rng.IntN(3) == 0,
				})
				if err != nil {
					return fmt.Errorf("review for %s: %w", o.key, err)
				}
				if o.review.reply != "" {
					const q = `
						INSERT INTO review_reply (review_id, author_id, body, created_at)
						VALUES (@review_id, @author_id, @body, @created_at)`
					if _, err := tx.Exec(ctx, q, pgx.NamedArgs{
						"review_id":  reviewID,
						"author_id":  parties[o.seller].id,
						"body":       o.review.reply,
						"created_at": o.review.at.Add(time.Duration(2+rng.IntN(30)) * time.Hour),
					}); err != nil {
						return fmt.Errorf("review reply for %s: %w", o.key, err)
					}
				}
				if err := writeVotes(ctx, tx, reviewID, *o.review, o.buyer, voters, parties); err != nil {
					return err
				}
				t := get(o.seller)
				t.reviewSum += int64(o.review.rating)
				t.reviewCount++
			}

			// Transaction feedback, on the completed orders only and not on all of them.
			if settled && rng.IntN(100) < 70 {
				rating := ratingFor(rng)
				if err := writeFeedback(ctx, tx, orderID, parties[o.buyer].id, parties[o.seller].id,
					"buyer-to-seller", rating, feedbackFromBuyer(rng), o.createdAt.Add(8*24*time.Hour)); err != nil {
					return err
				}
				t := get(o.seller)
				t.sellerFeedbackSum += int64(rating)
				t.sellerFeedback++

				if rng.IntN(100) < 55 {
					back := ratingFor(rng)
					if err := writeFeedback(ctx, tx, orderID, parties[o.seller].id, parties[o.buyer].id,
						"seller-to-buyer", back, feedbackFromSeller(rng), o.createdAt.Add(9*24*time.Hour)); err != nil {
						return err
					}
					b := get(o.buyer)
					b.buyerFeedbackSum += int64(back)
					b.buyerFeedback++
				}
			}
		}

		// Two rows per account, because every account here both sells and buys.
		// "reputation_reviews_are_seller_only" is why the review halves sit on one of them:
		// nobody reviews a buyer's products.
		for _, a := range seedAccounts {
			t := get(a.Key)
			const q = `
				INSERT INTO reputation (account_id, role, rating_sum, rating_count,
				                        review_rating_sum, review_rating_count,
				                        completed_orders, cancelled_orders, updated_at)
				VALUES (@account_id, @role, @rating_sum, @rating_count,
				        @review_rating_sum, @review_rating_count,
				        @completed_orders, @cancelled_orders, @now)
				ON CONFLICT (account_id, role) DO UPDATE SET
					rating_sum = EXCLUDED.rating_sum,
					rating_count = EXCLUDED.rating_count,
					review_rating_sum = EXCLUDED.review_rating_sum,
					review_rating_count = EXCLUDED.review_rating_count,
					completed_orders = EXCLUDED.completed_orders,
					cancelled_orders = EXCLUDED.cancelled_orders,
					updated_at = EXCLUDED.updated_at`
			if _, err := tx.Exec(ctx, q, pgx.NamedArgs{
				"account_id": parties[a.Key].id, "role": "seller",
				"rating_sum": t.sellerFeedbackSum, "rating_count": t.sellerFeedback,
				"review_rating_sum": t.reviewSum, "review_rating_count": t.reviewCount,
				"completed_orders": t.completedAsSeller, "cancelled_orders": t.cancelledSeller,
				"now": p.now,
			}); err != nil {
				return fmt.Errorf("seller reputation for %s: %w", a.Key, err)
			}
			if _, err := tx.Exec(ctx, q, pgx.NamedArgs{
				"account_id": parties[a.Key].id, "role": "buyer",
				"rating_sum": t.buyerFeedbackSum, "rating_count": t.buyerFeedback,
				"review_rating_sum": 0, "review_rating_count": 0,
				"completed_orders": t.completedAsBuyer, "cancelled_orders": t.cancelledBuyer,
				"now": p.now,
			}); err != nil {
				return fmt.Errorf("buyer reputation for %s: %w", a.Key, err)
			}
		}

		for _, t := range p.tickets {
			id, err := writeTicket(ctx, tx, t, p, parties, cat, sales)
			if err != nil {
				return err
			}
			ticketIDs[t.key] = id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ticketIDs, nil
}

type reviewRow struct {
	plan       reviewPlan
	listingID  int64
	orderID    int64
	author     int64
	seller     int64
	attachment int64
	withPhoto  bool
}

func writeReview(ctx context.Context, tx pgx.Tx, r reviewRow) (int64, error) {
	attachments := []int64{}
	if r.withPhoto {
		attachments = append(attachments, r.attachment)
	}
	replies := 0
	if r.plan.reply != "" {
		replies = 1
	}
	const q = `
		INSERT INTO review (listing_id, order_id, author_id, seller_id, rating, body,
		                    attachments, helpful_count, not_helpful_count, reply_count, created_at)
		VALUES (@listing_id, @order_id, @author_id, @seller_id, @rating, @body,
		        @attachments, @helpful, @not_helpful, @reply_count, @created_at)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"listing_id": r.listingID, "order_id": r.orderID,
		"author_id": r.author, "seller_id": r.seller,
		"rating": r.plan.rating, "body": r.plan.body,
		"attachments": attachments,
		"helpful":     r.plan.helpful, "not_helpful": r.plan.notHelpful,
		"reply_count": replies, "created_at": r.plan.at,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert review: %w", err)
	}
	return id, nil
}

// writeVotes puts as many rows behind the tally as the tally claims. The counters on the review
// are a denormalization of these — sorting a product's reviews by helpfulness has to be an
// index scan — so writing one without the other is exactly the drift the column exists to risk.
func writeVotes(ctx context.Context, tx pgx.Tx, reviewID int64, r reviewPlan, author string, voters []string, parties map[string]party) error {
	want := r.helpful + r.notHelpful
	if want == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	cast := 0
	for _, key := range voters {
		if cast == want {
			break
		}
		if key == author {
			continue // your own review is not something you get to find helpful
		}
		vote := 1
		if cast >= r.helpful {
			vote = -1
		}
		cast++
		batch.Queue(
			`INSERT INTO review_vote (review_id, account_id, vote, created_at)
			 VALUES (@review_id, @account_id, @vote, @created_at)
			 ON CONFLICT (review_id, account_id) DO NOTHING`,
			pgx.NamedArgs{
				"review_id": reviewID, "account_id": parties[key].id,
				"vote": vote, "created_at": r.at.Add(time.Duration(cast) * 6 * time.Hour),
			})
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert review votes: %w", err)
	}
	return nil
}

func writeFeedback(ctx context.Context, tx pgx.Tx, orderID, rater, ratee int64, direction string, rating int, comment string, at time.Time) error {
	const q = `
		INSERT INTO feedback (order_id, rater_id, ratee_id, direction, rating, comment,
		                      created_at, published_at)
		VALUES (@order_id, @rater_id, @ratee_id, @direction, @rating, @comment,
		        @created_at, @published_at)
		ON CONFLICT (order_id, direction) DO NOTHING`
	// Published, because the blind window is long past on every order old enough to have
	// feedback — and an unpublished row counts towards nothing and shows nowhere.
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"order_id": orderID, "rater_id": rater, "ratee_id": ratee,
		"direction": direction, "rating": rating, "comment": comment,
		"created_at": at, "published_at": at.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		return fmt.Errorf("insert feedback: %w", err)
	}
	return nil
}

func writeTicket(ctx context.Context, tx pgx.Tx, t ticketPlan, p *plan, parties map[string]party, cat catalogIDs, sales salesResult) (int64, error) {
	var refType, refID any
	switch t.refType {
	case "order":
		id, ok := sales.orderIDs[t.refOrder]
		if !ok {
			return 0, fmt.Errorf("ticket %s: no such order %q", t.key, t.refOrder)
		}
		refType, refID = t.refType, id
	case "listing":
		if t.refListing <= 0 || t.refListing > len(cat.listings) {
			return 0, fmt.Errorf("ticket %s: listing index %d out of range", t.key, t.refListing)
		}
		refType, refID = t.refType, cat.listings[t.refListing-1]
	case "":
		// A feature request is about nothing in particular, and both columns stay NULL.
	default:
		return 0, fmt.Errorf("ticket %s: unsupported ref type %q", t.key, t.refType)
	}

	var assignee, resolvedBy, action, note, resolvedAt any
	if t.assignee != "" {
		assignee = parties[t.assignee].id
	}
	if t.status == "resolved" {
		resolvedBy = parties[t.resolvedBy].id
		action = t.action
		note = t.note
		resolvedAt = t.createdAt.Add(30 * time.Hour)
	}

	const q = `
		INSERT INTO ticket (requester_id, kind, subject, ref_type, ref_id, reason, status,
		                    assignee_id, action_taken, resolved_by_id, resolved_at,
		                    resolution_note, created_at)
		VALUES (@requester_id, @kind, @subject, @ref_type, @ref_id, @reason, @status,
		        @assignee_id, @action_taken, @resolved_by_id, @resolved_at,
		        @resolution_note, @created_at)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"requester_id":    parties[t.requester].id,
		"kind":            t.kind,
		"subject":         t.subject,
		"ref_type":        refType,
		"ref_id":          refID,
		"reason":          dbx.NullText(t.reason),
		"status":          t.status,
		"assignee_id":     assignee,
		"action_taken":    action,
		"resolved_by_id":  resolvedBy,
		"resolved_at":     resolvedAt,
		"resolution_note": note,
		"created_at":      t.createdAt,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert ticket %s: %w", t.key, err)
	}
	return id, nil
}

// attachTicketThreads closes the loop chat opened: the ticket names its conversation, which is
// what makes the support screen able to find the thread at all. Written after chat, because the
// thread does not exist until then and the two tables are in different schemas.
func attachTicketThreads(ctx context.Context, pool *pgxpool.Pool, ticketIDs, threadIDs map[string]int64) error {
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		for key, ticketID := range ticketIDs {
			convoID, ok := threadIDs[key]
			if !ok {
				continue
			}
			const q = `UPDATE ticket SET conversation_id = @convo WHERE id = @id`
			if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"convo": convoID, "id": ticketID}); err != nil {
				return fmt.Errorf("attach thread to ticket %s: %w", key, err)
			}
		}
		return nil
	})
}

//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/infra/postgres"
	trustpg "shopnexus/internal/module/trust/adapter/postgres"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TRUST_DB_DSN")
	if dsn == "" {
		t.Skip("TRUST_DB_DSN not set")
	}
	return dsn
}

func newRepo(t *testing.T) (*trustpg.Repo, *pgxpool.Pool) {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "trust")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return trustpg.New(pool), pool
}

// party keeps one run's rows out of another's: these tables are append-only across runs, so a
// fixed account id would make every aggregate depend on history.
func party(t *testing.T) (buyer, seller, order int64) {
	t.Helper()
	base := time.Now().UnixNano() % 1_000_000_000
	return base, base + 1, base + 2
}

// The blind window's whole contract in one test: a first submission counts nothing, the second
// reveals both, and each is folded into the role its rater was rating.
func TestInsertFeedback_RevealsThePairAndCounts(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)

	fromBuyer, err := domain.NewFeedback(orderID, buyer, seller, domain.DirectionBuyerToSeller, 5, "fast")
	if err != nil {
		t.Fatalf("NewFeedback: %v", err)
	}
	if err := r.InsertFeedback(ctx, &fromBuyer); err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
	if fromBuyer.Published() {
		t.Fatal("a first rating is visible, so the second side can retaliate")
	}
	// Nothing is counted while it is blind.
	rep, err := r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.RatingCount != 0 {
		t.Fatalf("rating count = %d, want a blind rating uncounted", rep.RatingCount)
	}
	// One per direction.
	dup, err := domain.NewFeedback(orderID, buyer, seller, domain.DirectionBuyerToSeller, 1, "")
	if err != nil {
		t.Fatalf("NewFeedback: %v", err)
	}
	if err := r.InsertFeedback(ctx, &dup); !errors.Is(err, domain.ErrFeedbackExists) {
		t.Fatalf("second InsertFeedback = %v, want ErrFeedbackExists", err)
	}

	fromSeller, err := domain.NewFeedback(orderID, seller, buyer, domain.DirectionSellerToBuyer, 4, "")
	if err != nil {
		t.Fatalf("NewFeedback: %v", err)
	}
	if err := r.InsertFeedback(ctx, &fromSeller); err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
	// The pair completing reveals both in the same transaction.
	if !fromSeller.Published() {
		t.Error("the second rating is still blind with both sides in")
	}
	first, err := r.FindFeedback(ctx, orderID, domain.DirectionBuyerToSeller)
	if err != nil {
		t.Fatalf("FindFeedback: %v", err)
	}
	if !first.Published() {
		t.Error("the first rating was not revealed when the pair completed")
	}

	// And each landed on the role its rater was rating.
	rep, err = r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.RatingSum != 5 || rep.RatingCount != 1 {
		t.Errorf("seller reputation = %+v, want one 5", rep)
	}
	rep, err = r.FindReputation(ctx, buyer, domain.RoleBuyer)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.RatingSum != 4 || rep.RatingCount != 1 {
		t.Errorf("buyer reputation = %+v, want one 4", rep)
	}

	// A published rating is visible on the ratee's list; the role narrows to the side they
	// were rated on.
	rows, err := r.ListFeedback(ctx, port.FeedbackFilter{
		RateeID: seller, Role: domain.RoleSeller, Cursor: port.CursorFilter{Limit: 50},
	})
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("rows = %+v, want the buyer's published rating", rows)
	}
	// And the cursor over that list is strict on the pair, so the row it names is behind it.
	rest, err := r.ListFeedback(ctx, port.FeedbackFilter{
		RateeID: seller, Role: domain.RoleSeller,
		Cursor: port.CursorFilter{Before: rows[0].CreatedAt, BeforeID: rows[0].ID, Limit: 50},
	})
	if err != nil {
		t.Fatalf("ListFeedback past the cursor: %v", err)
	}
	if containsFeedback(rest, rows[0].ID) {
		t.Error("the row the cursor names is served on the next page too")
	}
	// The seller's own rating of the buyer is not on the seller's list.
	rows, err = r.ListFeedback(ctx, port.FeedbackFilter{
		RateeID: seller, Role: domain.RoleBuyer, Cursor: port.CursorFilter{Limit: 50},
	})
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want nothing rating this account as a buyer", rows)
	}
}

// The reveal list and its guard: a stale blind rating goes public, counts once, and a retried
// pass counts nothing more.
func TestPublishFeedback_CountsExactlyOnce(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)

	f, err := domain.NewFeedback(orderID, buyer, seller, domain.DirectionBuyerToSeller, 3, "")
	if err != nil {
		t.Fatalf("NewFeedback: %v", err)
	}
	if err := r.InsertFeedback(ctx, &f); err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
	if due, err := r.DueFeedback(ctx, time.Now(), 200); err != nil || containsFeedback(due, f.ID) {
		t.Fatalf("DueFeedback = %v, %v; want the window still open", ids(due), err)
	}
	// Wind the submission back past the window, as the clock would.
	past := time.Now().Add(-domain.BlindWindow - time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE feedback SET created_at = $1 WHERE id = $2`, past, f.ID); err != nil {
		t.Fatalf("backdate feedback: %v", err)
	}
	due, err := r.DueFeedback(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("DueFeedback: %v", err)
	}
	if !containsFeedback(due, f.ID) {
		t.Fatalf("DueFeedback = %v, want the stale rating due", ids(due))
	}

	now := time.Now()
	if err := r.PublishFeedback(ctx, f.ID, now); err != nil {
		t.Fatalf("PublishFeedback: %v", err)
	}
	// Again: the `published_at IS NULL` guard is what stops the second count.
	if err := r.PublishFeedback(ctx, f.ID, now); err != nil {
		t.Fatalf("second PublishFeedback: %v", err)
	}
	rep, err := r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.RatingCount != 1 || rep.RatingSum != 3 {
		t.Fatalf("reputation = %+v, want the reveal counted once", rep)
	}
	if due, err := r.DueFeedback(ctx, time.Now(), 200); err != nil || containsFeedback(due, f.ID) {
		t.Fatalf("DueFeedback = %v, %v; want it off the list", ids(due), err)
	}
}

// An account nobody has rated has a reputation of zeroes, not a missing row: a new seller's
// profile page is not an error.
func TestFindReputation_UnratedIsZero(t *testing.T) {
	r, _ := newRepo(t)
	_, seller, _ := party(t)
	rep, err := r.FindReputation(context.Background(), seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.AccountID != seller || rep.RatingCount != 0 || rep.AverageRating() != 0 {
		t.Fatalf("reputation = %+v, want an empty aggregate", rep)
	}
}

// The order counters both parties carry, each in the role they played — and once per order,
// because the settled event that drives them is at-least-once.
func TestAddOrderOutcome_CountsBothParties(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)

	if err := r.AddOrderOutcome(ctx, orderID, buyer, seller, true); err != nil {
		t.Fatalf("AddOrderOutcome: %v", err)
	}
	// The same order again is a redelivery, which the key it wrote makes a no-op.
	if err := r.AddOrderOutcome(ctx, orderID, buyer, seller, true); err != nil {
		t.Fatalf("second AddOrderOutcome: %v", err)
	}
	if err := r.AddOrderOutcome(ctx, orderID+1, buyer, seller, false); err != nil {
		t.Fatalf("AddOrderOutcome: %v", err)
	}
	for _, party := range []struct {
		id   int64
		role string
	}{{buyer, domain.RoleBuyer}, {seller, domain.RoleSeller}} {
		rep, err := r.FindReputation(ctx, party.id, party.role)
		if err != nil {
			t.Fatalf("FindReputation: %v", err)
		}
		if rep.CompletedOrders != 1 || rep.CancelledOrders != 1 {
			t.Errorf("%s counters = %d/%d, want one each", party.role,
				rep.CompletedOrders, rep.CancelledOrders)
		}
	}
}

// A review folds into the seller's own pair of columns and never into the transaction pair —
// one order can produce both, and summing them would count it twice.
func TestReview_RatingIsCountedApart(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)
	listingID := orderID + 100

	f, err := domain.NewFeedback(orderID, buyer, seller, domain.DirectionBuyerToSeller, 5, "")
	if err != nil {
		t.Fatalf("NewFeedback: %v", err)
	}
	if err := r.InsertFeedback(ctx, &f); err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
	if err := r.PublishFeedback(ctx, f.ID, time.Now()); err != nil {
		t.Fatalf("PublishFeedback: %v", err)
	}

	v, err := domain.NewReview(listingID, orderID, buyer, seller, 1, "not as described", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v); err != nil {
		t.Fatalf("InsertReview: %v", err)
	}
	// One review per (listing, author, order).
	dup, err := domain.NewReview(listingID, orderID, buyer, seller, 3, "again", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &dup); !errors.Is(err, domain.ErrReviewExists) {
		t.Fatalf("second InsertReview = %v, want ErrReviewExists", err)
	}

	rep, err := r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.RatingSum != 5 || rep.RatingCount != 1 {
		t.Errorf("transaction rating = %d over %d, want one 5", rep.RatingSum, rep.RatingCount)
	}
	if rep.ReviewRatingSum != 1 || rep.ReviewRatingCount != 1 {
		t.Errorf("review rating = %d over %d, want one 1", rep.ReviewRatingSum, rep.ReviewRatingCount)
	}

	// An edit moves the aggregate by the difference, in the same transaction as the row.
	if err := v.SetRating(5); err != nil {
		t.Fatalf("SetRating: %v", err)
	}
	if err := r.SaveReview(ctx, v); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	rep, err = r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.ReviewRatingSum != 5 || rep.ReviewRatingCount != 1 {
		t.Errorf("review rating = %d over %d, want the edit folded in", rep.ReviewRatingSum, rep.ReviewRatingCount)
	}
	reread, err := r.FindReview(ctx, v.ID)
	if err != nil {
		t.Fatalf("FindReview: %v", err)
	}
	if reread.Rating != 5 || reread.UpdatedAt == nil {
		t.Fatalf("review = %+v, want the edit recorded", reread)
	}

	// The average catalog caches is read off the same rows.
	average, count, err := r.ReviewAverage(ctx, listingID)
	if err != nil {
		t.Fatalf("ReviewAverage: %v", err)
	}
	if average != 5 || count != 1 {
		t.Fatalf("average = %v over %d, want 5 over one", average, count)
	}

	// Removal takes it back out and drops the row.
	if err := r.DeleteReview(ctx, v.ID); err != nil {
		t.Fatalf("DeleteReview: %v", err)
	}
	rep, err = r.FindReputation(ctx, seller, domain.RoleSeller)
	if err != nil {
		t.Fatalf("FindReputation: %v", err)
	}
	if rep.ReviewRatingSum != 0 || rep.ReviewRatingCount != 0 {
		t.Errorf("review rating = %d over %d, want it gone", rep.ReviewRatingSum, rep.ReviewRatingCount)
	}
	if _, err := r.FindReview(ctx, v.ID); !errors.Is(err, domain.ErrReviewNotFound) {
		t.Fatalf("FindReview after delete = %v, want ErrReviewNotFound", err)
	}
	// And the transaction rating is untouched: the two pairs are independent.
	if rep.RatingSum != 5 || rep.RatingCount != 1 {
		t.Errorf("transaction rating = %d over %d, want it untouched", rep.RatingSum, rep.RatingCount)
	}
}

// The thread: the count moves with the rows, the cap is per review rather than per page, and a
// deleted review takes its replies with it.
func TestReplies_CountedAndCappedPerReview(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)
	listingID := orderID + 100

	v, err := domain.NewReview(listingID, orderID, buyer, seller, 4, "good", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v); err != nil {
		t.Fatalf("InsertReview: %v", err)
	}
	var replyIDs []int64
	for i := range 4 {
		reply, err := domain.NewReviewReply(v.ID, seller, "answer")
		if err != nil {
			t.Fatalf("NewReviewReply: %v", err)
		}
		if err := r.InsertReply(ctx, &reply); err != nil {
			t.Fatalf("InsertReply %d: %v", i, err)
		}
		replyIDs = append(replyIDs, reply.ID)
	}
	reread, err := r.FindReview(ctx, v.ID)
	if err != nil {
		t.Fatalf("FindReview: %v", err)
	}
	if reread.ReplyCount != 4 {
		t.Fatalf("reply count = %d, want 4", reread.ReplyCount)
	}

	// The cap applies to each thread, which is what a page of reviews needs.
	capped, err := r.ListReplies(ctx, []int64{v.ID}, 3)
	if err != nil {
		t.Fatalf("ListReplies: %v", err)
	}
	if len(capped[v.ID]) != 3 {
		t.Fatalf("capped thread = %d, want 3", len(capped[v.ID]))
	}
	whole, err := r.ListReplies(ctx, []int64{v.ID}, 0)
	if err != nil {
		t.Fatalf("ListReplies: %v", err)
	}
	if len(whole[v.ID]) != 4 {
		t.Fatalf("whole thread = %d, want 4", len(whole[v.ID]))
	}
	// Oldest first, so the conversation reads in order.
	if whole[v.ID][0].ID != replyIDs[0] {
		t.Errorf("thread starts at %d, want the first reply %d", whole[v.ID][0].ID, replyIDs[0])
	}

	if err := r.DeleteReply(ctx, replyIDs[0]); err != nil {
		t.Fatalf("DeleteReply: %v", err)
	}
	reread, err = r.FindReview(ctx, v.ID)
	if err != nil {
		t.Fatalf("FindReview: %v", err)
	}
	if reread.ReplyCount != 3 {
		t.Fatalf("reply count = %d, want 3 after the delete", reread.ReplyCount)
	}

	// The review going takes the rest of the thread with it, by cascade.
	if err := r.DeleteReview(ctx, v.ID); err != nil {
		t.Fatalf("DeleteReview: %v", err)
	}
	if _, err := r.FindReply(ctx, replyIDs[1]); !errors.Is(err, domain.ErrReplyNotFound) {
		t.Fatalf("FindReply after the review went = %v, want ErrReplyNotFound", err)
	}
}

// One vote per account, replaced in place: flipping moves one unit between the two totals
// rather than adding a second, and withdrawing removes the row.
func TestVotes_FlipMovesOneUnit(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)
	listingID := orderID + 100
	voter := buyer + 500

	v, err := domain.NewReview(listingID, orderID, buyer, seller, 4, "good", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v); err != nil {
		t.Fatalf("InsertReview: %v", err)
	}

	up, err := domain.NewReviewVote(v.ID, voter, domain.VoteHelpful)
	if err != nil {
		t.Fatalf("NewReviewVote: %v", err)
	}
	tally, err := r.PutVote(ctx, up)
	if err != nil {
		t.Fatalf("PutVote: %v", err)
	}
	if tally.Helpful != 1 || tally.NotHelpful != 0 {
		t.Fatalf("tally = %+v, want one helpful", tally)
	}
	// The same vote again is the state the caller asked for, not a second unit.
	tally, err = r.PutVote(ctx, up)
	if err != nil {
		t.Fatalf("second PutVote: %v", err)
	}
	if tally.Helpful != 1 {
		t.Fatalf("tally = %+v, want the repeat counted once", tally)
	}

	down, err := domain.NewReviewVote(v.ID, voter, domain.VoteNotHelpful)
	if err != nil {
		t.Fatalf("NewReviewVote: %v", err)
	}
	tally, err = r.PutVote(ctx, down)
	if err != nil {
		t.Fatalf("PutVote: %v", err)
	}
	if tally.Helpful != 0 || tally.NotHelpful != 1 {
		t.Fatalf("tally = %+v, want the vote moved rather than added", tally)
	}
	mine, err := r.MyVotes(ctx, voter, []int64{v.ID})
	if err != nil {
		t.Fatalf("MyVotes: %v", err)
	}
	if mine[v.ID] != domain.VoteNotHelpful {
		t.Fatalf("my vote = %d, want the flipped one", mine[v.ID])
	}

	tally, err = r.DeleteVote(ctx, v.ID, voter)
	if err != nil {
		t.Fatalf("DeleteVote: %v", err)
	}
	if tally.Helpful != 0 || tally.NotHelpful != 0 {
		t.Fatalf("tally = %+v, want the vote gone", tally)
	}
	if _, err := r.DeleteVote(ctx, v.ID, voter); !errors.Is(err, domain.ErrVoteNotFound) {
		t.Fatalf("second DeleteVote = %v, want ErrVoteNotFound", err)
	}
}

// The ticket queue: one open ticket per requester per target, the queue is the unresolved
// slice, and the status a transition moves from is what stops two verdicts.
func TestTickets_QueueAndVerdict(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	requester, other, target := party(t)
	refListing, refID := domain.RefListing, target
	reason := "counterfeit"

	first, err := domain.NewTicket(requester, domain.KindReportListing, "Hang gia", &refListing, &refID, &reason)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := r.InsertTicket(ctx, &first); err != nil {
		t.Fatalf("InsertTicket: %v", err)
	}
	dup, err := domain.NewTicket(requester, domain.KindReportListing, "Hang gia lan hai", &refListing, &refID, &reason)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := r.InsertTicket(ctx, &dup); !errors.Is(err, domain.ErrTicketExists) {
		t.Fatalf("second InsertTicket = %v, want ErrTicketExists", err)
	}
	// A different requester naming the same target is the pattern, not a duplicate.
	second, err := domain.NewTicket(other, domain.KindReportListing, "Hang gia", &refListing, &refID, &reason)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := r.InsertTicket(ctx, &second); err != nil {
		t.Fatalf("InsertTicket: %v", err)
	}
	listingTarget := port.TicketTarget{RefType: domain.RefListing, RefID: target}
	counts, err := r.CountOpenAgainst(ctx, []port.TicketTarget{listingTarget})
	if err != nil {
		t.Fatalf("CountOpenAgainst: %v", err)
	}
	if count := counts[listingTarget]; count != 2 {
		t.Fatalf("open against the target = %d, want 2", count)
	}
	// Tickets are found by what they are about — that is how order's verdict closes the ones a
	// disputed refund opened. Every open one, oldest first: the unique index is per requester, so two
	// people naming one target is two tickets and one verdict has to answer both.
	found, err := r.OpenTicketsAgainst(ctx, domain.RefListing, target)
	if err != nil {
		t.Fatalf("OpenTicketsAgainst: %v", err)
	}
	if len(found) != 2 || found[0].ID != first.ID || found[1].ID != second.ID {
		t.Fatalf("by ref = %+v, want both open tickets oldest first (%d, %d)", found, first.ID, second.ID)
	}

	// The queue's default slice, oldest first: the order it is worked.
	queue, err := r.ListTickets(ctx, port.TicketFilter{
		Statuses: []string{domain.StatusOpen, domain.StatusReviewing},
		Kind:     domain.KindReportListing,
		Cursor:   port.CursorFilter{Limit: 200},
	})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if !containsTicket(queue, first.ID) || !containsTicket(queue, second.ID) {
		t.Fatalf("queue is missing one of the open tickets")
	}
	// The queue runs forward, so its cursor is the row a page ended at and the tuple is strict:
	// the ticket it names is not served again, and one that shares its timestamp still is.
	last := queue[len(queue)-1]
	rest, err := r.ListTickets(ctx, port.TicketFilter{
		Statuses: []string{domain.StatusOpen, domain.StatusReviewing},
		Kind:     domain.KindReportListing,
		Cursor:   port.CursorFilter{Before: last.CreatedAt, BeforeID: last.ID, Limit: 200},
	})
	if err != nil {
		t.Fatalf("ListTickets past the cursor: %v", err)
	}
	if containsTicket(rest, last.ID) {
		t.Error("the row the cursor names is served on the next page too")
	}

	moderator := requester + 900
	// Claiming is guarded by `open`, so two moderators claiming at once means one wins.
	if err := first.Claim(moderator); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := r.SaveTicket(ctx, first, []string{domain.StatusOpen}); err != nil {
		t.Fatalf("SaveTicket: %v", err)
	}
	if err := r.SaveTicket(ctx, first, []string{domain.StatusOpen}); !errors.Is(err, domain.ErrTicketResolved) {
		t.Fatalf("second claim = %v, want the guard to refuse it", err)
	}

	if err := first.Resolve(moderator, domain.ActionListingRemoved, "confirmed"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	from := []string{domain.StatusOpen, domain.StatusReviewing}
	if err := r.SaveTicket(ctx, first, from); err != nil {
		t.Fatalf("SaveTicket: %v", err)
	}
	stored, err := r.FindTicket(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindTicket: %v", err)
	}
	if !stored.Resolved() || stored.ActionTaken == nil || *stored.ActionTaken != domain.ActionListingRemoved {
		t.Fatalf("ticket = %+v, want a recorded verdict", stored)
	}
	if stored.ResolvedByID == nil || *stored.ResolvedByID != moderator || stored.ResolvedAt == nil {
		t.Fatalf("ticket = %+v, want the moderator recorded", stored)
	}
	// A resolved ticket leaves the queue and stops counting towards the pattern.
	queue, err = r.ListTickets(ctx, port.TicketFilter{
		Statuses: from, Kind: domain.KindReportListing, Cursor: port.CursorFilter{Limit: 200},
	})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if containsTicket(queue, first.ID) {
		t.Error("a resolved ticket is still in the queue")
	}
	counts, err = r.CountOpenAgainst(ctx, []port.TicketTarget{listingTarget})
	if err != nil {
		t.Fatalf("CountOpenAgainst: %v", err)
	}
	if count := counts[listingTarget]; count != 1 {
		t.Fatalf("open against the target = %d, want 1", count)
	}
	// And the requester may raise it again now that the first case is closed.
	scam := "scam"
	again, err := domain.NewTicket(requester, domain.KindReportListing, "Hang gia", &refListing, &refID, &scam)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := r.InsertTicket(ctx, &again); err != nil {
		t.Fatalf("InsertTicket after the verdict: %v", err)
	}

	// A ticket about nothing in particular is one the target index does not hold: two support
	// requests from the same person are two tickets.
	for range 2 {
		ask, err := domain.NewTicket(requester, domain.KindFeatureRequest, "Loc theo tinh", nil, nil, nil)
		if err != nil {
			t.Fatalf("NewTicket: %v", err)
		}
		if err := r.InsertTicket(ctx, &ask); err != nil {
			t.Fatalf("InsertTicket for a feature request: %v", err)
		}
	}

	// The thread's id is recorded by a second write, so it lands without touching the rest of the
	// row — that is the repair path for a ticket whose conversation was not written.
	live, err := r.FindTicket(ctx, again.ID)
	if err != nil {
		t.Fatalf("FindTicket: %v", err)
	}
	live.AttachThread(again.ID + 7000)
	if err := r.SaveTicket(ctx, live, []string{domain.StatusOpen}); err != nil {
		t.Fatalf("SaveTicket with a thread: %v", err)
	}
	withThread, err := r.FindTicket(ctx, again.ID)
	if err != nil {
		t.Fatalf("FindTicket: %v", err)
	}
	if withThread.ConversationID == nil || *withThread.ConversationID != again.ID+7000 {
		t.Fatalf("ticket = %+v, want the conversation recorded", withThread)
	}
}

// Two writers that move one review's rating is write skew unless the row is locked: an edit
// and a delete that each computed their delta from the same 5 take the aggregate down by 9 for
// a review worth 5, which the non-negative CHECK then answers with a 500 — or, once the seller
// has other reviews to absorb it, with a number nobody can reproduce.
//
// One attempt is not enough to observe it: the window is between the two statements, and the
// first pair of goroutines rarely overlaps. Unguarded, this loop leaves a wrong aggregate
// within a few iterations; guarded, on none.
func TestSaveAndDeleteReview_CannotBothMoveTheAggregate(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)

	const attempts = 40
	for i := range attempts {
		// A seller per attempt, so one iteration's aggregate is not the next one's history.
		attemptSeller := seller + int64(i)*1_000
		v, err := domain.NewReview(orderID+100+int64(i), orderID+int64(i), buyer, attemptSeller,
			5, "as described", nil)
		if err != nil {
			t.Fatalf("NewReview: %v", err)
		}
		if err := r.InsertReview(ctx, &v); err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		// Both writers hold the same copy of the review, rated 5.
		edited := v
		if err := edited.SetRating(1); err != nil {
			t.Fatalf("SetRating: %v", err)
		}

		done := make(chan error, 2)
		start := make(chan struct{})
		go func() {
			<-start
			done <- r.SaveReview(ctx, edited)
		}()
		go func() {
			<-start
			done <- r.DeleteReview(ctx, v.ID)
		}()
		close(start)
		for range 2 {
			// A delete that landed first makes the edit a not-found, which is the honest answer.
			if err := <-done; err != nil && !errors.Is(err, domain.ErrReviewNotFound) {
				t.Fatalf("attempt %d: %v", i, err)
			}
		}

		rep, err := r.FindReputation(ctx, attemptSeller, domain.RoleSeller)
		if err != nil {
			t.Fatalf("FindReputation: %v", err)
		}
		// Whichever order they ran in, the review is gone and so is its rating.
		if rep.ReviewRatingSum != 0 || rep.ReviewRatingCount != 0 {
			t.Fatalf("attempt %d: review rating = %d over %d, want the deleted review counted for nothing",
				i, rep.ReviewRatingSum, rep.ReviewRatingCount)
		}
	}
}

// Both sides submitting at the same moment is write skew too: looking for the counterpart is a
// read, so under READ COMMITTED each transaction misses the other's uncommitted row, neither
// reveals, and the pair stays blind until the sweep two weeks later. The advisory lock on the
// order is what makes the second one see the first.
func TestInsertFeedback_SimultaneousSubmissionsStillReveal(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)

	const attempts = 40
	for i := range attempts {
		// A party and an order per attempt: the pair is unique per order, and a reputation
		// carries across iterations otherwise.
		b, s, o := buyer+int64(i)*1_000, seller+int64(i)*1_000, orderID+int64(i)*1_000
		fromBuyer, err := domain.NewFeedback(o, b, s, domain.DirectionBuyerToSeller, 5, "")
		if err != nil {
			t.Fatalf("NewFeedback: %v", err)
		}
		fromSeller, err := domain.NewFeedback(o, s, b, domain.DirectionSellerToBuyer, 4, "")
		if err != nil {
			t.Fatalf("NewFeedback: %v", err)
		}

		done := make(chan error, 2)
		start := make(chan struct{})
		for _, row := range []*domain.Feedback{&fromBuyer, &fromSeller} {
			go func() {
				<-start
				done <- r.InsertFeedback(ctx, row)
			}()
		}
		close(start)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatalf("attempt %d: InsertFeedback: %v", i, err)
			}
		}

		for _, direction := range []string{domain.DirectionBuyerToSeller, domain.DirectionSellerToBuyer} {
			stored, err := r.FindFeedback(ctx, o, direction)
			if err != nil {
				t.Fatalf("FindFeedback: %v", err)
			}
			if !stored.Published() {
				t.Fatalf("attempt %d: %s is still blind with both sides in", i, direction)
			}
		}
		// Published is counted, and counted once.
		rep, err := r.FindReputation(ctx, s, domain.RoleSeller)
		if err != nil {
			t.Fatalf("FindReputation: %v", err)
		}
		if rep.RatingSum != 5 || rep.RatingCount != 1 {
			t.Fatalf("attempt %d: seller reputation = %d over %d, want one 5",
				i, rep.RatingSum, rep.RatingCount)
		}
		rep, err = r.FindReputation(ctx, b, domain.RoleBuyer)
		if err != nil {
			t.Fatalf("FindReputation: %v", err)
		}
		if rep.RatingSum != 4 || rep.RatingCount != 1 {
			t.Fatalf("attempt %d: buyer reputation = %d over %d, want one 4",
				i, rep.RatingSum, rep.RatingCount)
		}
	}
}

// Each sort pages on the key it orders by, and the cursor carries the row id beside that key —
// so a page boundary between two rows that share a timestamp exactly, which one transaction's
// writes do, reaches both.
func TestListReviews_PagesEverySortWithoutSkipping(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	buyer, seller, orderID := party(t)
	listingID := orderID + 100

	var written []int64
	for i := range 3 {
		v, err := domain.NewReview(listingID, orderID+int64(i), buyer, seller, 5, "good", nil)
		if err != nil {
			t.Fatalf("NewReview: %v", err)
		}
		if err := r.InsertReview(ctx, &v); err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		written = append(written, v.ID)
	}
	// One timestamp for all three, as a single transaction would have given them, and a tally
	// whose order disagrees with the timestamps.
	if _, err := pool.Exec(ctx, `UPDATE review SET created_at = now(),
	                             helpful_count = 100 - array_position(@ids::bigint[], id)
	                             WHERE listing_id = @listing_id`,
		pgx.NamedArgs{"ids": written, "listing_id": listingID}); err != nil {
		t.Fatalf("level the timestamps: %v", err)
	}

	for _, sort := range []string{domain.ReviewSortNewest, domain.ReviewSortHelpful} {
		seen := map[int64]bool{}
		cursor := port.CursorFilter{Limit: 2}
		for range 4 {
			// Limit 2 is one row plus the extra read the service uses to detect another page.
			rows, err := r.ListReviews(ctx, port.ReviewFilter{
				ListingID: listingID, Sort: sort, Cursor: cursor,
			})
			if err != nil {
				t.Fatalf("ListReviews(%s): %v", sort, err)
			}
			if len(rows) == 0 {
				break
			}
			last := rows[0]
			seen[last.ID] = true
			if len(rows) == 1 {
				break
			}
			cursor = port.CursorFilter{
				Before: last.CreatedAt, BeforeCount: last.HelpfulCount,
				BeforeID: last.ID, Limit: 2,
			}
		}
		for _, id := range written {
			if !seen[id] {
				t.Fatalf("sort=%s: review %d was unreachable across the pages (saw %v)", sort, id, seen)
			}
		}
	}
}

func ids(rows []domain.Feedback) []int64 {
	out := make([]int64, len(rows))
	for i, row := range rows {
		out[i] = row.ID
	}
	return out
}

func containsFeedback(rows []domain.Feedback, id int64) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

func containsTicket(rows []domain.Ticket, id int64) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

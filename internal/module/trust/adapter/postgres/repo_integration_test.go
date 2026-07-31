//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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

// The order counters both parties carry, each in the role they played.
func TestAddOrderOutcome_CountsBothParties(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller, _ := party(t)

	if err := r.AddOrderOutcome(ctx, buyer, seller, true); err != nil {
		t.Fatalf("AddOrderOutcome: %v", err)
	}
	if err := r.AddOrderOutcome(ctx, buyer, seller, false); err != nil {
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

	v, err := domain.NewReview(listingID, orderID, buyer, 1, "not as described", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v, seller); err != nil {
		t.Fatalf("InsertReview: %v", err)
	}
	// One review per (listing, author, order).
	dup, err := domain.NewReview(listingID, orderID, buyer, 3, "again", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &dup, seller); !errors.Is(err, domain.ErrReviewExists) {
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
	if err := r.SaveReview(ctx, v, seller, 4); err != nil {
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
	if err := r.DeleteReview(ctx, v.ID, seller, reread.Rating); err != nil {
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

	v, err := domain.NewReview(listingID, orderID, buyer, 4, "good", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v, seller); err != nil {
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
	if err := r.DeleteReview(ctx, v.ID, seller, reread.Rating); err != nil {
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

	v, err := domain.NewReview(listingID, orderID, buyer, 4, "good", nil)
	if err != nil {
		t.Fatalf("NewReview: %v", err)
	}
	if err := r.InsertReview(ctx, &v, seller); err != nil {
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

// The report queue: one open report per reporter per target, the queue is the unresolved
// slice, and the status a transition moves from is what stops two verdicts.
func TestReports_QueueAndVerdict(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	reporter, other, target := party(t)

	first, err := domain.NewReport(reporter, domain.ReportRefListing, target, "counterfeit", "same photos")
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	if err := r.InsertReport(ctx, &first); err != nil {
		t.Fatalf("InsertReport: %v", err)
	}
	dup, err := domain.NewReport(reporter, domain.ReportRefListing, target, "scam", "")
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	if err := r.InsertReport(ctx, &dup); !errors.Is(err, domain.ErrReportExists) {
		t.Fatalf("second InsertReport = %v, want ErrReportExists", err)
	}
	// A different reporter naming the same target is the pattern, not a duplicate.
	second, err := domain.NewReport(other, domain.ReportRefListing, target, "counterfeit", "")
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	if err := r.InsertReport(ctx, &second); err != nil {
		t.Fatalf("InsertReport: %v", err)
	}
	count, err := r.CountOpenAgainst(ctx, domain.ReportRefListing, target)
	if err != nil {
		t.Fatalf("CountOpenAgainst: %v", err)
	}
	if count != 2 {
		t.Fatalf("open against the target = %d, want 2", count)
	}

	// The queue's default slice, oldest first: the order it is worked.
	queue, err := r.ListReports(ctx, port.ReportFilter{
		Statuses: []string{domain.ReportStatusOpen, domain.ReportStatusReviewing},
		RefType:  domain.ReportRefListing,
		Cursor:   port.CursorFilter{Limit: 200},
	})
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if !containsReport(queue, first.ID) || !containsReport(queue, second.ID) {
		t.Fatalf("queue is missing one of the open reports")
	}

	// Claiming is guarded by `open`, so two moderators claiming at once means one wins.
	if err := first.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := r.SaveReport(ctx, first, []string{domain.ReportStatusOpen}); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	if err := r.SaveReport(ctx, first, []string{domain.ReportStatusOpen}); !errors.Is(err, domain.ErrReportResolved) {
		t.Fatalf("second claim = %v, want the guard to refuse it", err)
	}

	moderator := reporter + 900
	if err := first.Resolve(moderator, domain.ReportStatusActioned, domain.ActionListingRemoved, "confirmed"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	from := []string{domain.ReportStatusOpen, domain.ReportStatusReviewing}
	if err := r.SaveReport(ctx, first, from); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	stored, err := r.FindReport(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindReport: %v", err)
	}
	if !stored.Resolved() || stored.ActionTaken == nil || *stored.ActionTaken != domain.ActionListingRemoved {
		t.Fatalf("report = %+v, want a recorded verdict", stored)
	}
	if stored.ResolvedByID == nil || *stored.ResolvedByID != moderator || stored.ResolvedAt == nil {
		t.Fatalf("report = %+v, want the moderator recorded", stored)
	}
	// A resolved report leaves the queue and stops counting towards the pattern.
	queue, err = r.ListReports(ctx, port.ReportFilter{
		Statuses: from, RefType: domain.ReportRefListing, Cursor: port.CursorFilter{Limit: 200},
	})
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if containsReport(queue, first.ID) {
		t.Error("a resolved report is still in the queue")
	}
	count, err = r.CountOpenAgainst(ctx, domain.ReportRefListing, target)
	if err != nil {
		t.Fatalf("CountOpenAgainst: %v", err)
	}
	if count != 1 {
		t.Fatalf("open against the target = %d, want 1", count)
	}
	// And the reporter may file again now that the first case is closed.
	again, err := domain.NewReport(reporter, domain.ReportRefListing, target, "scam", "")
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	if err := r.InsertReport(ctx, &again); err != nil {
		t.Fatalf("InsertReport after the verdict: %v", err)
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

func containsReport(rows []domain.Report, id int64) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

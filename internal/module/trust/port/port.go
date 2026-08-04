// Package port: the interface the trust adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/trust/domain"
)

// CursorFilter pages a list on the key its ORDER BY uses plus the row id, compared as a
// tuple. Not an offset: these lists move under the reader, and an offset would skip or repeat
// a row when they do.
//
// The id is half the key, not decoration: CURRENT_TIMESTAMP is transaction-scoped, so rows
// written by one statement share a timestamp exactly, and a bare `created_at < @before` drops
// whichever of them the previous page did not reach. A cursor is present iff BeforeID is set —
// every row has an id, so that holds for a counter cursor of 0 too.
type CursorFilter struct {
	// Before is the timestamp the previous page ended at, for a list ordered by one.
	Before time.Time
	// BeforeCount is the counter the previous page ended at, for a list ordered by one
	// (a review page sorted by helpfulness). Zero for the rest.
	BeforeCount int64
	// BeforeID is the id of that last row, which is what breaks a tie on the key.
	BeforeID int64
	Limit    int
}

// FeedbackFilter reads the published feedback an account received, optionally in one role.
type FeedbackFilter struct {
	RateeID int64
	// Role narrows to how the account was rated: seller feedback is not the same account's
	// buying record.
	Role   string
	Cursor CursorFilter
}

// ReviewFilter is a listing's review page. Sort is the whitelist the adapter switches on,
// so an ordering a client invents never reaches the SQL — and it also picks which key the
// cursor compares, because paging on one column while ordering by another skips rows.
type ReviewFilter struct {
	ListingID int64
	Rating    int16
	Sort      string
	Cursor    CursorFilter
}

// TicketFilter serves both the requester's own list and the moderator queue. Statuses is a
// set because the queue's default is "open or reviewing" — the predicate its partial index
// covers.
type TicketFilter struct {
	RequesterID int64
	Statuses    []string
	// Kind narrows the queue to one sort of work — the abuse reports, the refund disputes — which
	// is how one table stays one queue without making a moderator read all of it.
	Kind    string
	RefType string
	Cursor  CursorFilter
}

// TicketTarget is one polymorphic target of a ticket. The pair is what a count is per, and
// what a page of the queue asks for in one query.
type TicketTarget struct {
	RefType string
	RefID   int64
}

// VoteTally is a review's two counters plus the caller's own vote, which is what a vote
// write answers with.
type VoteTally struct {
	Helpful    int64
	NotHelpful int64
	// MyVote is nil when the caller has not voted.
	MyVote *int16
}

type Repository interface {
	// --- feedback ---
	// InsertFeedback writes one direction's rating. It publishes both rows in the same
	// transaction when this submission completes the pair, because a reveal that lands
	// half-applied shows one side a rating the other cannot yet see.
	InsertFeedback(ctx context.Context, f *domain.Feedback) error
	FindFeedback(ctx context.Context, orderID int64, direction string) (domain.Feedback, error)
	// OrderFeedback is both directions of one order, however many exist.
	OrderFeedback(ctx context.Context, orderID int64) ([]domain.Feedback, error)
	ListFeedback(ctx context.Context, f FeedbackFilter) ([]domain.Feedback, error)
	// DueFeedback is the reveal list: blind rows whose window has run out. A durable timer
	// per row would do the same job; this exists so a sweep can catch what a timer lost.
	DueFeedback(ctx context.Context, now time.Time, limit int) ([]domain.Feedback, error)
	// PublishFeedback reveals a row and folds its rating into the ratee's reputation in one
	// transaction, so a published rating is always a counted one.
	PublishFeedback(ctx context.Context, id int64, at time.Time) error

	// --- reputation ---
	FindReputation(ctx context.Context, accountID int64, role string) (domain.Reputation, error)
	// AddOrderOutcome bumps the completed or cancelled counter of both parties. Driven by
	// order's settled event, which is at-least-once, so the order id is recorded in the same
	// transaction and a redelivery bumps nothing.
	AddOrderOutcome(ctx context.Context, orderID, buyerID, sellerID int64, completed bool) error
	// ReviewAverage is a listing's rating over its reviews, which catalog caches. Returned
	// with the count, because an average with no count cannot be rendered honestly.
	ReviewAverage(ctx context.Context, listingID int64) (average float64, count int64, err error)

	// --- reviews ---
	// InsertReview writes the review and folds its rating into the seller's reputation in
	// one transaction. The seller is the review's own SellerID, frozen from the order.
	InsertReview(ctx context.Context, r *domain.Review) error
	FindReview(ctx context.Context, id int64) (domain.Review, error)
	ListReviews(ctx context.Context, f ReviewFilter) ([]domain.Review, error)
	// SaveReview writes an edit and moves the seller's review rating by the difference in the
	// same transaction — a rating changed from 5 to 1 that leaves the aggregate alone is a
	// number nobody can reproduce. The difference is derived from the row under its own lock,
	// never from the caller's copy: two edits that each computed a delta from the same read
	// move the aggregate twice.
	SaveReview(ctx context.Context, r domain.Review) error
	// DeleteReview drops the review, its replies and its votes (by cascade) and takes its
	// rating back out of the seller's reputation — again, the rating on the locked row.
	DeleteReview(ctx context.Context, id int64) error

	// --- replies ---
	// InsertReply writes the reply and bumps the review's reply_count in one transaction.
	InsertReply(ctx context.Context, r *domain.ReviewReply) error
	FindReply(ctx context.Context, id int64) (domain.ReviewReply, error)
	// ListReplies is one review's thread, oldest first. limit = 0 means all of it, which is
	// what the single-review read wants and a page of reviews must not have.
	ListReplies(ctx context.Context, reviewIDs []int64, limit int) (map[int64][]domain.ReviewReply, error)
	DeleteReply(ctx context.Context, id int64) error

	// --- votes ---
	// PutVote records or replaces a vote and moves both counters by the delta in the same
	// transaction: a tally that drifted from its rows is recomputable, a half-applied flip
	// is visible on the page.
	PutVote(ctx context.Context, v domain.ReviewVote) (VoteTally, error)
	DeleteVote(ctx context.Context, reviewID, accountID int64) (VoteTally, error)
	// MyVotes is one account's votes across a page of reviews, so a page is one query
	// rather than one per row.
	MyVotes(ctx context.Context, accountID int64, reviewIDs []int64) (map[int64]int16, error)

	// --- reports ---
	InsertTicket(ctx context.Context, t *domain.Ticket) error
	FindTicket(ctx context.Context, id int64) (domain.Ticket, error)
	// FindTicketByRef answers the open ticket about one target, which is how the module that owns
	// that target finds the ticket to close when it decides.
	FindTicketByRef(ctx context.Context, refType string, refID int64) (domain.Ticket, error)
	ListTickets(ctx context.Context, f TicketFilter) ([]domain.Ticket, error)
	// SaveReport writes a claim or a verdict, guarded by the status it moves from: a stale
	// read loses instead of overwriting a decision it never saw.
	SaveTicket(ctx context.Context, t domain.Ticket, from []string) error
	// CountOpenAgainst is how many unresolved reports name each target — a moderator decides
	// on a pattern rather than on one complaint. A whole page of targets in one query, so the
	// queue does not cost a round trip per row.
	CountOpenAgainst(ctx context.Context, targets []TicketTarget) (map[TicketTarget]int64, error)
}

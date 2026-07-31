package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Review sort orders. `helpful` reads the stored tally, which is why that tally is a
// column: sorting a product's reviews has to be an ordered index scan.
const (
	ReviewSortNewest  = "newest"
	ReviewSortHelpful = "helpful"
)

// The two votes. There is no neutral value — withdrawing a vote deletes the row, because a
// stored zero is a row that says nothing and would have to be excluded from every tally.
const (
	VoteHelpful    int16 = 1
	VoteNotHelpful int16 = -1
)

// Review is a buyer's rating of a product they bought. OrderID is required by design: no
// purchase, no review — and one review per (listing, author, order), so buying the same
// thing twice earns a second review but one purchase does not earn two.
//
// The three counters are denormalized from review_reply and review_vote for the same
// reason catalog caches a listing's rating: a tally computed per query is neither
// indexable nor seekable by a cursor.
type Review struct {
	ID        int64
	ListingID int64 `validate:"required"`
	OrderID   int64 `validate:"required"`
	AuthorID  int64 `validate:"required"`
	Rating    int16 `validate:"required,gte=1,lte=5"`
	Body      string
	// Attachments are photos of the item as received — resource ids of this module's own
	// uploads, held inline for the same reason catalog and chat do it.
	Attachments     []int64
	HelpfulCount    int64
	NotHelpfulCount int64
	ReplyCount      int64
	CreatedAt       time.Time
	// UpdatedAt is nil until the author edits it. A review that was rewritten after the
	// seller answered it should say so.
	UpdatedAt *time.Time
}

func NewReview(listingID, orderID, authorID int64, rating int16, body string, attachments []int64) (Review, error) {
	r := Review{
		ListingID: listingID, OrderID: orderID, AuthorID: authorID,
		Rating: rating, Body: body, Attachments: attachments,
	}
	if err := validation.Default().Struct(r); err != nil {
		return Review{}, validation.AsError(err)
	}
	return r, nil
}

// SetRating, SetBody and SetAttachments are the edits the author may make. Each records
// the edit, because "was this the text the seller replied to" is a question the thread
// cannot answer on its own.
func (r *Review) SetRating(rating int16) error {
	if rating < 1 || rating > 5 {
		return ErrReviewRatingRange
	}
	r.Rating = rating
	r.touch()
	return nil
}

func (r *Review) SetBody(body string) error {
	if len(body) > 2000 {
		return ErrReviewBodyTooLong
	}
	r.Body = body
	r.touch()
	return nil
}

func (r *Review) SetAttachments(attachments []int64) {
	r.Attachments = attachments
	r.touch()
}

func (r *Review) touch() { r.UpdatedAt = new(time.Now()) }

// MutableBy reports whether an account may edit or delete this review. A moderator acting
// on an upheld report may delete it, which the service checks separately — this is the
// author's own right.
func (r Review) MutableBy(accountID int64) bool { return r.AuthorID == accountID }

// ReviewReply is a flat answer under a review: a seller responding, a buyer following up.
// No rating and no order — a reply is not a review.
type ReviewReply struct {
	ID        int64
	ReviewID  int64  `validate:"required"`
	AuthorID  int64  `validate:"required"`
	Body      string `validate:"required,max=2000"`
	CreatedAt time.Time
}

func NewReviewReply(reviewID, authorID int64, body string) (ReviewReply, error) {
	r := ReviewReply{ReviewID: reviewID, AuthorID: authorID, Body: body}
	if err := validation.Default().Struct(r); err != nil {
		return ReviewReply{}, validation.AsError(err)
	}
	return r, nil
}

// ReviewVote is one account's helpfulness verdict on one review. The pair is the whole
// row, so it is the key.
type ReviewVote struct {
	ReviewID  int64
	AccountID int64
	Vote      int16
	CreatedAt time.Time
}

func NewReviewVote(reviewID, accountID int64, vote int16) (ReviewVote, error) {
	if vote != VoteHelpful && vote != VoteNotHelpful {
		return ReviewVote{}, ErrVoteValue
	}
	return ReviewVote{ReviewID: reviewID, AccountID: accountID, Vote: vote}, nil
}

// VoteDelta is what a vote change does to a review's two counters. Returned as a pair
// rather than applied here, because the counters and the vote row have to move in one
// transaction: a tally that drifted from its rows is recomputable, but a half-applied flip
// is visible on the page.
//
// previous is the caller's existing vote, 0 when they had none; next is what they want, 0
// to withdraw.
func VoteDelta(previous, next int16) (helpful, notHelpful int64) {
	helpful = countOf(next, VoteHelpful) - countOf(previous, VoteHelpful)
	notHelpful = countOf(next, VoteNotHelpful) - countOf(previous, VoteNotHelpful)
	return helpful, notHelpful
}

func countOf(vote, want int16) int64 {
	if vote == want {
		return 1
	}
	return 0
}

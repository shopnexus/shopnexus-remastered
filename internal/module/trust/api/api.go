// Package trustapi is the published contract of the trust service: two-way order
// feedback, product reviews with their replies and helpfulness votes, per-account
// reputation, and abuse reports with a moderator surface.
//
// Two ratings that are deliberately not the same thing. Feedback rates the other party of
// one order and runs in both directions; a review rates the product and only a buyer
// writes it. They are counted apart, because one order can produce both and adding them
// would count that order twice.
package trustapi

import (
	"context"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// CursorInfo is the cursor meta every list here answers with. A timestamp cursor, not an
// offset: these lists move under the reader.
type CursorInfo struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// ---------------------------------------------------------------- feedback ---

type Feedback struct {
	ID        id.ID[id.Feedback]        `json:"id"`
	OrderID   id.ID[id.Order]           `json:"order_id"`
	Rater     accountapi.AccountSummary `json:"rater"`
	RateeID   id.ID[id.Account]         `json:"ratee_id"`
	Direction string                    `json:"direction"`
	Rating    int16                     `json:"rating"`
	Comment   string                    `json:"comment"`
	// PublishedAt is null while the rating is still blind. Only published feedback is
	// visible to anyone but its author, and only published feedback is counted.
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type FeedbackPage struct {
	Data []Feedback `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// OrderFeedback is both directions of one order, as far as the caller may see them.
type OrderFeedback struct {
	Mine *Feedback `json:"mine"`
	// Theirs arrives only once published; while it is blind this stays null and
	// TheirsSubmitted carries as much as can be told without breaking blindness.
	Theirs          *Feedback `json:"theirs"`
	TheirsSubmitted bool      `json:"theirs_submitted"`
	// RevealAt is when the blind window closes and whatever was submitted becomes visible.
	// Null once there is nothing left waiting — a client that cannot count down has to
	// guess.
	RevealAt *time.Time `json:"reveal_at"`
}

type OrderFeedbackRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	OrderID id.ID[id.Order]   `json:"-" validate:"required"`
}

// SubmitFeedbackRequest carries neither direction nor ratee: both follow from which side of
// the order the caller is on, so nobody can file feedback as the other party.
type SubmitFeedbackRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	OrderID id.ID[id.Order]   `json:"-" validate:"required"`
	Rating  int16             `json:"rating" validate:"required,gte=1,lte=5"`
	Comment string            `json:"comment,omitempty" validate:"max=2000"`
}

type ListFeedbackRequest struct {
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Role      string            `json:"-" validate:"omitempty,oneof=seller buyer"`
	Cursor    string            `json:"-"`
	Limit     int               `json:"-" validate:"required,gt=0,lte=100"`
}

// -------------------------------------------------------------- reputation ---

type Reputation struct {
	AccountID           id.ID[id.Account] `json:"account_id"`
	Role                string            `json:"role"`
	RatingAverage       float64           `json:"rating_average"`
	RatingCount         int64             `json:"rating_count"`
	ReviewRatingAverage float64           `json:"review_rating_average"`
	ReviewRatingCount   int64             `json:"review_rating_count"`
	CompletedOrders     int64             `json:"completed_orders"`
	CancelledOrders     int64             `json:"cancelled_orders"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type GetReputationRequest struct {
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Role      string            `json:"-" validate:"required,oneof=seller buyer"`
}

// ----------------------------------------------------------------- reviews ---

// VoteTally is the counters on the review, not a count of vote rows per request — that is
// what lets sort=helpful be an index scan.
type VoteTally struct {
	Helpful    int64 `json:"helpful"`
	NotHelpful int64 `json:"not_helpful"`
	// MyVote is the caller's own vote, null when they have not voted or are anonymous.
	MyVote *int16 `json:"my_vote"`
}

type ReviewReply struct {
	ID     id.ID[id.ReviewReply]     `json:"id"`
	Author accountapi.AccountSummary `json:"author"`
	// IsSeller says whether the author owns the listing, which is what a reader wants to
	// know about an answer.
	IsSeller  bool      `json:"is_seller"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Review struct {
	ID          id.ID[id.Review]          `json:"id"`
	ListingID   id.ID[id.Listing]         `json:"listing_id"`
	Author      accountapi.AccountSummary `json:"author"`
	Rating      int16                     `json:"rating"`
	Body        string                    `json:"body"`
	Attachments []common.ResourceDTO      `json:"attachments"`
	// Replies is the first few, oldest first, capped on a listing page. ReplyCount says how
	// many there are and GET /reviews/{id} returns the rest.
	Replies    []ReviewReply `json:"replies"`
	ReplyCount int64         `json:"reply_count"`
	Votes      VoteTally     `json:"votes"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  *time.Time    `json:"updated_at"`
}

type ReviewPage struct {
	Data []Review   `json:"data"`
	Meta CursorInfo `json:"meta"`
}

type ListReviewsRequest struct {
	ListingID id.ID[id.Listing] `json:"-" validate:"required"`
	// ViewerID is the caller when there is one: it fills in my_vote. Reading is public.
	ViewerID id.ID[id.Account] `json:"-"`
	Rating   int16             `json:"-" validate:"omitempty,gte=1,lte=5"`
	Sort     string            `json:"-" validate:"omitempty,oneof=newest helpful"`
	Cursor   string            `json:"-"`
	Limit    int               `json:"-" validate:"required,gt=0,lte=100"`
}

type SubmitReviewRequest struct {
	ActorID     id.ID[id.Account]    `json:"-" validate:"required"`
	ListingID   id.ID[id.Listing]    `json:"-" validate:"required"`
	OrderID     id.ID[id.Order]      `json:"order_id" validate:"required"`
	Rating      int16                `json:"rating" validate:"required,gte=1,lte=5"`
	Body        string               `json:"body,omitempty" validate:"max=2000"`
	Attachments []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
}

type GetReviewRequest struct {
	ID       id.ID[id.Review]  `json:"-" validate:"required"`
	ViewerID id.ID[id.Account] `json:"-"`
}

// UpdateReviewRequest is every field optional. Attachments are replaced wholesale rather
// than patched: a photo set is one fact, and there is nothing to clear separately.
type UpdateReviewRequest struct {
	ActorID     id.ID[id.Account]     `json:"-" validate:"required"`
	ID          id.ID[id.Review]      `json:"-" validate:"required"`
	Rating      *int16                `json:"rating,omitempty" validate:"omitempty,gte=1,lte=5"`
	Body        *string               `json:"body,omitempty" validate:"omitempty,max=2000"`
	Attachments *[]id.ID[id.Resource] `json:"attachments,omitempty" validate:"omitempty,max=10"`
}

type ReviewRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Review]  `json:"-" validate:"required"`
}

type SubmitReplyRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	ReviewID id.ID[id.Review]  `json:"-" validate:"required"`
	Body     string            `json:"body" validate:"required,max=2000"`
}

type ReplyRequest struct {
	ActorID id.ID[id.Account]     `json:"-" validate:"required"`
	ID      id.ID[id.ReviewReply] `json:"-" validate:"required"`
}

type VoteReviewRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Review]  `json:"-" validate:"required"`
	// Vote is 1 or -1. There is no neutral value — withdrawing a vote is a delete.
	Vote int16 `json:"vote" validate:"required,oneof=-1 1"`
}

// CreateUploadRequest asks for a slot to PUT a review photo into. The bytes never pass
// through the API: the answer is a short-lived signed URL, and a second call confirms the
// row once the object is there — so a review can never render a photo whose bytes never
// arrived.
type CreateUploadRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Filename string            `json:"filename" validate:"required,max=255"`
	// Mime and Size are what the client is about to send. Both are checked before a byte
	// moves: a slot signed for anything is a slot for anything.
	Mime string `json:"mime" validate:"required,max=100"`
	Size int64  `json:"size" validate:"required,gt=0"`
}

// UploadSlot is where to PUT, what to confirm afterwards, and until when.
type UploadSlot struct {
	ResourceID id.ID[id.Resource] `json:"resource_id"`
	URL        string             `json:"url"`
	// Headers the client must send with the PUT, when the signature covers any.
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// ConfirmUploadRequest is the second step. The size is read from the store rather than
// taken from the client, so what it declared cannot become the record.
type ConfirmUploadRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.Resource] `json:"-" validate:"required"`
}

// ----------------------------------------------------------------- reports ---

// Report's RefID is an opaque id whose kind is given by RefType, so it is a string here and
// the service encodes it with that kind's prefix.
type Report struct {
	ID      id.ID[id.Report] `json:"id"`
	RefType string           `json:"ref_type"`
	RefID   string           `json:"ref_id"`
	Reason  string           `json:"reason"`
	Detail  string           `json:"detail"`
	Status  string           `json:"status"`
	// ActionTaken and the two resolution fields are null until a moderator decides.
	ActionTaken    *string    `json:"action_taken"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ResolutionNote *string    `json:"resolution_note"`
	CreatedAt      time.Time  `json:"created_at"`
}

type ReportPage struct {
	Data []Report   `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// AdminReport is a queue entry: a moderator needs the reporter and the target beside the
// report itself, and how many others named the same target.
type AdminReport struct {
	Report     Report                     `json:"report"`
	Reporter   accountapi.AccountSummary  `json:"reporter"`
	ResolvedBy *accountapi.AccountSummary `json:"resolved_by"`
	// OpenReportsAgainstTarget is the pattern a decision rests on rather than one
	// complaint.
	OpenReportsAgainstTarget int64 `json:"open_reports_against_target"`
	// Target is the reported content, shaped by RefType and fetched from the module that
	// owns it. Null when that module no longer has it — a listing already taken down.
	Target map[string]any `json:"target,omitempty"`
}

type AdminReportPage struct {
	Data []AdminReport `json:"data"`
	Meta CursorInfo    `json:"meta"`
}

// SubmitReportRequest's RefID is opaque and kinded by RefType, so the two are validated
// together.
type SubmitReportRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	RefType string            `json:"ref_type" validate:"required,oneof=listing account message review review-reply"`
	RefID   string            `json:"ref_id" validate:"required"`
	Reason  string            `json:"reason" validate:"required,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Detail  string            `json:"detail,omitempty" validate:"max=2000"`
}

type ListReportsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"omitempty,oneof=open reviewing actioned dismissed"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,gt=0,lte=100"`
}

// AdminListReportsRequest defaults to the open and under-review slice — the predicate the
// queue's partial index covers.
type AdminListReportsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"omitempty,oneof=open reviewing actioned dismissed"`
	RefType string            `json:"-" validate:"omitempty,oneof=listing account message review review-reply"`
	Reason  string            `json:"-" validate:"omitempty,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,gt=0,lte=100"`
}

type ReportRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Report]  `json:"-" validate:"required"`
}

type ResolveReportRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Report]  `json:"-" validate:"required"`
	// Status is a verdict, so open and reviewing are not choices.
	Status string `json:"status" validate:"required,oneof=actioned dismissed"`
	// ActionTaken is required: `none` goes with a dismissal, and upholding a report names
	// what was done about it.
	ActionTaken string `json:"action_taken" validate:"required,oneof=none listing-removed message-removed account-suspended warning"`
	Note        string `json:"note,omitempty" validate:"max=2000"`
}

// RecordOrderOutcomeRequest is order's settled event in this module's terms. OrderID is what
// makes a redelivery a no-op, so it is required rather than informational.
type RecordOrderOutcomeRequest struct {
	OrderID  id.ID[id.Order]   `validate:"required"`
	BuyerID  id.ID[id.Account] `validate:"required"`
	SellerID id.ID[id.Account] `validate:"required"`
	// Completed tells a payout from a cancellation.
	Completed bool
}

type Service interface {
	// --- feedback ---
	GetOrderFeedback(ctx context.Context, req OrderFeedbackRequest) (OrderFeedback, error)
	SubmitFeedback(ctx context.Context, req SubmitFeedbackRequest) (Feedback, error)
	ListAccountFeedback(ctx context.Context, req ListFeedbackRequest) (FeedbackPage, error)

	// --- reputation: recomputed, never written through this API ---
	GetReputation(ctx context.Context, req GetReputationRequest) (Reputation, error)

	// --- reviews ---
	ListReviews(ctx context.Context, req ListReviewsRequest) (ReviewPage, error)
	SubmitReview(ctx context.Context, req SubmitReviewRequest) (Review, error)
	GetReview(ctx context.Context, req GetReviewRequest) (Review, error)
	UpdateReview(ctx context.Context, req UpdateReviewRequest) (Review, error)
	DeleteReview(ctx context.Context, req ReviewRequest) error
	SubmitReply(ctx context.Context, req SubmitReplyRequest) (ReviewReply, error)
	DeleteReply(ctx context.Context, req ReplyRequest) error
	VoteReview(ctx context.Context, req VoteReviewRequest) (VoteTally, error)
	UnvoteReview(ctx context.Context, req ReviewRequest) (VoteTally, error)

	// --- uploads: a review photo, in two steps ---
	// CreateUpload reserves a row and a presigned slot; ConfirmUpload makes it real once the
	// bytes are at the store. Until then the resource resolves to nothing, so a
	// half-finished upload cannot be attached to anything.
	CreateUpload(ctx context.Context, req CreateUploadRequest) (UploadSlot, error)
	ConfirmUpload(ctx context.Context, req ConfirmUploadRequest) (common.ResourceDTO, error)

	// --- reports ---
	SubmitReport(ctx context.Context, req SubmitReportRequest) (Report, error)
	ListMyReports(ctx context.Context, req ListReportsRequest) (ReportPage, error)
	AdminListReports(ctx context.Context, req AdminListReportsRequest) (AdminReportPage, error)
	AdminGetReport(ctx context.Context, req ReportRequest) (AdminReport, error)
	AdminClaimReport(ctx context.Context, req ReportRequest) (Report, error)
	AdminResolveReport(ctx context.Context, req ResolveReportRequest) (Report, error)

	// --- driven by the durable workflow, not by a route ---
	//
	// Idempotent and safe to call again: a journaled step is retried, so a second call has
	// to be a no-op rather than a second effect.

	// RevealDueFeedback publishes blind ratings whose window has run out, so a party who
	// never rates cannot keep the other's rating hidden for ever.
	RevealDueFeedback(ctx context.Context, limit int) (int, error)

	// RecordOrderOutcome folds a finished order into both parties' counters. Driven by
	// order's settled event: "152 completed, 3 cancelled" says something an average cannot.
	// Once per order id, recorded with the bump, so a redelivered settlement counts nothing.
	RecordOrderOutcome(ctx context.Context, req RecordOrderOutcomeRequest) error
}

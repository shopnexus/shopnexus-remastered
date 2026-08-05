// Package trustapi is the published contract of the trust service: two-way order
// feedback, product reviews with their replies and helpfulness votes, per-account
// reputation, and the tickets users raise — with a moderator surface over them.
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

// The two roles a reputation is kept for: the same account is rated separately as a seller and as
// a buyer, so every read names one. Published because the gateway defaults to one of them.
const (
	RoleSeller = "seller"
	RoleBuyer  = "buyer"
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

// ----------------------------------------------------------------- tickets ---

// Ticket is one thing the requester raised. RefID is an opaque id whose kind is given by RefType, so
// it is a string here and the service encodes it with that kind's prefix.
//
// The conversation is ConversationID: the requester's own words and photos are its first message,
// and replies go through the ordinary chat routes. Who answered is never here — support answers as
// the desk.
type Ticket struct {
	ID      id.ID[id.Ticket] `json:"id"`
	Kind    string           `json:"kind"`
	Subject string           `json:"subject"`
	// RefType and RefID are null on a ticket about nothing in particular — a feature request.
	RefType *string `json:"ref_type"`
	RefID   *string `json:"ref_id"`
	// Reason is a report's, and null on every other kind.
	Reason *string `json:"reason"`
	Status string  `json:"status"`
	// ConversationID is null only in the moment between the ticket being written and its thread
	// being opened; a read repairs it.
	ConversationID *id.ID[id.Conversation] `json:"conversation_id"`
	// ActionTaken and the two resolution fields are null until a moderator decides. `none` is a
	// ticket answered with nothing done.
	ActionTaken    *string    `json:"action_taken"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ResolutionNote *string    `json:"resolution_note"`
	CreatedAt      time.Time  `json:"created_at"`
}

type TicketPage struct {
	Data []Ticket   `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// AdminTicket is a queue entry: a moderator needs the requester and the target beside the ticket
// itself, and how many others named the same target.
type AdminTicket struct {
	Ticket     Ticket                     `json:"ticket"`
	Requester  accountapi.AccountSummary  `json:"requester"`
	Assignee   *accountapi.AccountSummary `json:"assignee"`
	ResolvedBy *accountapi.AccountSummary `json:"resolved_by"`
	// OpenTicketsAgainstTarget is the pattern a decision rests on rather than one complaint.
	OpenTicketsAgainstTarget int64 `json:"open_tickets_against_target"`
	// Target is what the ticket is about, shaped by RefType and fetched from the module that owns
	// it. Null when that module no longer has it — a listing already taken down.
	Target map[string]any `json:"target,omitempty"`
}

type AdminTicketPage struct {
	Data []AdminTicket `json:"data"`
	Meta CursorInfo    `json:"meta"`
}

// OpenTicketRequest is everything a requester sends: what it is about, and their first message.
// Body and Attachments are not stored on the ticket — they are the thread's opening message, which
// is why raising a ticket needs no upload path of its own.
type OpenTicketRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Kind    string            `json:"kind" validate:"required,oneof=report-listing report-account report-message report-review report-review-reply refund-dispute order-issue payment account feature-request other"`
	Subject string            `json:"subject" validate:"required,min=1,max=200"`
	// RefID is opaque and kinded by Kind, so the two are validated together: a report about a
	// listing needs a listing id, and a feature request needs none.
	RefID string `json:"ref_id,omitempty"`
	// Reason is required for the report kinds and refused on every other, since a reason is what a
	// report says is wrong. Checked against Kind, so it cannot be a `required` tag here.
	Reason      string               `json:"reason,omitempty" validate:"omitempty,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Body        string               `json:"body,omitempty" validate:"max=4000"`
	Attachments []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
}

type ListTicketsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"omitempty,oneof=open reviewing resolved"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,gt=0,lte=100"`
}

// AdminListTicketsRequest defaults to the open and under-review slice — the predicate the queue's
// partial index covers.
type AdminListTicketsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"omitempty,oneof=open reviewing resolved"`
	Kind    string            `json:"-" validate:"omitempty,oneof=report-listing report-account report-message report-review report-review-reply refund-dispute order-issue payment account feature-request other"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,gt=0,lte=100"`
}

type TicketRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Ticket]  `json:"-" validate:"required"`
}

// RecordRefundVerdictRequest is order's verdict, arriving on the bus. The refund names the tickets
// it closes — however many: only one open ticket per *requester* per target is held, so both parties
// to one refund may have escalated it and a single verdict answers all of them.
type RecordRefundVerdictRequest struct {
	RefundID    id.ID[id.Refund]  `json:"-" validate:"required"`
	ModeratorID id.ID[id.Account] `json:"-" validate:"required"`
	BuyerWins   bool              `json:"-"`
	Note        string            `json:"-" validate:"max=2000"`
}

type ResolveTicketRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Ticket]  `json:"-" validate:"required"`
	// ActionTaken is what was done. `none` is the turn-down, which is why it is a value here rather
	// than a second status: a ticket read and answered with nothing done is still answered.
	//
	// Narrower than the ticket's own action: the two `refund-*` values are what order's verdict
	// records on its way out, so accepting one here would let a listing report be closed as a refund
	// nobody paid.
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

	// --- tickets: reports, refund disputes and support requests, one queue ---
	OpenTicket(ctx context.Context, req OpenTicketRequest) (Ticket, error)
	ListMyTickets(ctx context.Context, req ListTicketsRequest) (TicketPage, error)
	GetTicket(ctx context.Context, req TicketRequest) (Ticket, error)
	AdminListTickets(ctx context.Context, req AdminListTicketsRequest) (AdminTicketPage, error)
	AdminGetTicket(ctx context.Context, req TicketRequest) (AdminTicket, error)
	AdminClaimTicket(ctx context.Context, req TicketRequest) (Ticket, error)
	// RecordRefundVerdict closes the ticket a refund dispute opened, on order's published verdict.
	// Not a route: the decision is made by deciding the refund.
	RecordRefundVerdict(ctx context.Context, req RecordRefundVerdictRequest) error

	AdminResolveTicket(ctx context.Context, req ResolveTicketRequest) (Ticket, error)

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

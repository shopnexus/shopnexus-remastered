// Package orderapi is the published contract of the order service: the cart, the purchase
// session, the negotiation, the order itself, its shipment and its refunds.
//
// Two shapes recur. Checkout is pay-first and **the money creates the order**: finance's
// session completing is what writes it, so there is no endpoint for that and no seller
// confirmation anywhere. And a state transition is a POST to a noun sub-resource, never a
// PATCH of a status field — an order has no status column, only outcome facts.
package orderapi

import (
	"context"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// The two sides of every order, as the `role` query parameter names them. A list route reads one
// or the other, and the service branches on this value — named here so the branch and the
// contract cannot drift apart.
const (
	RoleBuyer  = "buyer"
	RoleSeller = "seller"
)

// The states Order.State reports, and the `state` filter's values. An order has no status column:
// these are derived from the outcome facts, and trust gates a rating and a review on them — so
// they are published rather than left for each reader to spell.
const (
	StateOpen      = "open"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
)

// --- responses ---

type CartItem struct {
	ID        id.ID[id.CartItem] `json:"id"`
	ListingID id.ID[id.Listing]  `json:"listing_id"`
	VariantID id.ID[id.Variant]  `json:"variant_id"`
	Quantity  int64              `json:"quantity"`
	CreatedAt time.Time          `json:"created_at"`
}

// Draft is a purchase session: the terms frozen when it opened, so a listing that showed
// 100k cannot charge a newly-updated price at checkout.
type Draft struct {
	ID         id.ID[id.DraftOrder] `json:"id"`
	ListingID  id.ID[id.Listing]    `json:"listing_id"`
	SellerID   id.ID[id.Account]    `json:"seller_id"`
	Name       string               `json:"name"`
	Currency   string               `json:"currency"`
	PriceMode  string               `json:"price_mode"`
	Variants   []DraftVariant       `json:"variants"`
	CreatedAt  time.Time            `json:"created_at"`
	ValidUntil time.Time            `json:"valid_until"`
	// CancelledAt is set when the buyer closed it, or the expiry did.
	CancelledAt *time.Time `json:"cancelled_at"`
}

type DraftVariant struct {
	VariantID  id.ID[id.Variant] `json:"variant_id"`
	Price      int64             `json:"price"`
	Attributes map[string]any    `json:"attributes,omitempty"`
}

type DraftPage struct {
	Data []Draft    `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// Item is one purchased line. OrderID is null between the checkout and the money landing —
// that window is the only thing it means.
type Item struct {
	ID               id.ID[id.Item]           `json:"id"`
	OrderID          *id.ID[id.Order]         `json:"order_id"`
	ListingID        id.ID[id.Listing]        `json:"listing_id"`
	VariantID        id.ID[id.Variant]        `json:"variant_id"`
	SellerID         id.ID[id.Account]        `json:"seller_id"`
	Quantity         int64                    `json:"quantity"`
	Currency         string                   `json:"currency"`
	TotalAmount      int64                    `json:"total_amount"`
	TransportOption  string                   `json:"transport_option"`
	PaymentSessionID id.ID[id.PaymentSession] `json:"payment_session_id"`
	Note             string                   `json:"note,omitempty"`
	CancelledAt      *time.Time               `json:"cancelled_at"`
	CreatedAt        time.Time                `json:"created_at"`
}

type ItemPage struct {
	Data []Item     `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// Offer is a negotiation. The conversation is in chat; these are the terms on the table.
type Offer struct {
	ID        id.ID[id.Offer]   `json:"id"`
	ListingID id.ID[id.Listing] `json:"listing_id"`
	VariantID id.ID[id.Variant] `json:"variant_id"`
	BuyerID   id.ID[id.Account] `json:"buyer_id"`
	SellerID  id.ID[id.Account] `json:"seller_id"`
	// AuthorID owns the standing proposal, which is whose turn it is *not*.
	AuthorID  id.ID[id.Account] `json:"author_id"`
	Status    string            `json:"status"`
	Quantity  int64             `json:"quantity"`
	Total     int64             `json:"total"`
	Currency  string            `json:"currency"`
	Reason    string            `json:"reason,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type OfferPage struct {
	Data []Offer    `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// Order is the purchase. No status column: the state is read from the two outcome
// timestamps, and the payout deadline is computed from the receipt.
type Order struct {
	ID id.ID[id.Order] `json:"id"`
	// Exactly one of these is set: a fixed-price sale came from a checkout, a negotiated
	// one from the offer both sides accepted.
	DraftID       *id.ID[id.DraftOrder]     `json:"draft_id"`
	OfferID       *id.ID[id.Offer]          `json:"offer_id"`
	Buyer         accountapi.AccountSummary `json:"buyer"`
	Seller        accountapi.AccountSummary `json:"seller"`
	Address       AddressSnapshot           `json:"address"`
	PickupAddress AddressSnapshot           `json:"pickup_address"`
	Items         []Item                    `json:"items"`
	State         string                    `json:"state"`
	Total         int64                     `json:"total"`
	Currency      string                    `json:"currency"`
	Transport     *Transport                `json:"transport,omitempty"`
	ReceivedAt    *time.Time                `json:"received_at"`
	// ReceiptAttachments is the unboxing evidence, captured with the receipt and never
	// added to: a refund is judged on what the buyer showed at that moment.
	ReceiptAttachments []common.ResourceDTO `json:"receipt_attachments"`
	// PayoutDeadlineAt is received_at + the escrow window, computed rather than stored.
	PayoutDeadlineAt *time.Time `json:"payout_deadline_at"`
	// PayoutReleasedAt is when the escrow reached the seller. Null on a completed order means
	// the release has not landed yet — the platform owes the seller and knows it.
	PayoutReleasedAt *time.Time `json:"payout_released_at"`
	CreatedAt        time.Time  `json:"created_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	CancelledAt      *time.Time `json:"cancelled_at"`
}

type OrderPage struct {
	Data []Order    `json:"data"`
	Meta CursorInfo `json:"meta"`
}

type AddressSnapshot struct {
	FullName      string  `json:"full_name"`
	Phone         string  `json:"phone"`
	Country       string  `json:"country"`
	ProvinceCode  string  `json:"province_code,omitempty"`
	DistrictCode  *string `json:"district_code,omitempty"`
	WardCode      string  `json:"ward_code,omitempty"`
	AddressDetail *string `json:"address_detail,omitempty"`
}

type Transport struct {
	ID     id.ID[id.Transport] `json:"id"`
	Option string              `json:"option"`
	Status string              `json:"status"`
	// Fee is what the buyer paid for delivery, on both a fixed-price and a negotiated sale.
	Fee       int64     `json:"fee"`
	CreatedAt time.Time `json:"created_at"`
}

// Refund always covers the whole order. Every non-terminal status is named for the party it
// waits on, and the deadline says when they run out of time.
type Refund struct {
	ID          id.ID[id.Refund]     `json:"id"`
	OrderID     id.ID[id.Order]      `json:"order_id"`
	BuyerID     id.ID[id.Account]    `json:"buyer_id"`
	Status      string               `json:"status"`
	Reason      string               `json:"reason"`
	Attachments []common.ResourceDTO `json:"attachments"`
	DeadlineAt  *time.Time           `json:"deadline_at"`
	// RejectionReason is what separates a refusal from a seller who let the window pass:
	// both land on the buyer, only one has a reason to show them.
	RejectionReason *string    `json:"rejection_reason"`
	SellerDecidedAt *time.Time `json:"seller_decided_at"`
	ReturnedAt      *time.Time `json:"returned_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type RefundPage struct {
	Data []Refund   `json:"data"`
	Meta CursorInfo `json:"meta"`
}

type Dispute struct {
	ID        id.ID[id.RefundDispute] `json:"id"`
	RefundID  id.ID[id.Refund]        `json:"refund_id"`
	Round     int16                   `json:"round"`
	OpenedBy  id.ID[id.Account]       `json:"opened_by"`
	Status    string                  `json:"status"`
	Reason    string                  `json:"reason"`
	Note      string                  `json:"note,omitempty"`
	CreatedAt time.Time               `json:"created_at"`
	RuledAt   *time.Time              `json:"ruled_at"`
}

type DisputePage struct {
	Data []Dispute  `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// CheckoutResult is the bill, itemised: what the goods cost, what delivery costs, and the total
// the payment session will collect. Both halves are shown because the buyer pays both — on a
// fixed-price sale and a negotiated one alike — and a total with no breakdown is a number nobody
// can check.
type CheckoutResult struct {
	Items          []Item                   `json:"items"`
	PaymentSession id.ID[id.PaymentSession] `json:"payment_session_id"`
	// GoodsTotal is the items; ShippingFee is what the carrier quoted for this parcel to this
	// address; Total is what will be charged.
	GoodsTotal  int64  `json:"goods_total"`
	ShippingFee int64  `json:"shipping_fee"`
	Total       int64  `json:"total"`
	Currency    string `json:"currency"`
}

// CursorInfo is the cursor meta every order list answers with.
type CursorInfo struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// --- requests ---

type ListCartRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type AddCartItemRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	VariantID id.ID[id.Variant] `json:"variant_id" validate:"required"`
	Quantity  int64             `json:"quantity" validate:"required,gt=0"`
}

type UpdateCartItemRequest struct {
	ActorID  id.ID[id.Account]  `json:"-" validate:"required"`
	ID       id.ID[id.CartItem] `json:"-" validate:"required"`
	Quantity int64              `json:"quantity" validate:"required,gt=0"`
}

type CartItemRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.CartItem] `json:"-" validate:"required"`
}

type CreateDraftRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	ListingID id.ID[id.Listing] `json:"listing_id" validate:"required"`
}

type ListDraftsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type DraftRequest struct {
	ActorID id.ID[id.Account]    `json:"-" validate:"required"`
	ID      id.ID[id.DraftOrder] `json:"-" validate:"required"`
}

// CheckoutLine is one variant and how many of it.
type CheckoutLine struct {
	VariantID id.ID[id.Variant] `json:"variant_id" validate:"required"`
	Quantity  int64             `json:"quantity" validate:"required,gt=0"`
}

type CheckoutRequest struct {
	ActorID id.ID[id.Account]    `json:"-" validate:"required"`
	ID      id.ID[id.DraftOrder] `json:"-" validate:"required"`
	Lines   []CheckoutLine       `json:"lines" validate:"required,min=1,dive"`
	// ContactID is the delivery address, snapshotted into every line: one session covers
	// one listing and therefore one seller, so a single address is correct.
	ContactID id.ID[id.Contact] `json:"contact_id" validate:"required"`
	// TransportOption is the carrier. The buyer pays delivery, so the trade-off is theirs.
	TransportOption string `json:"transport_option" validate:"required,max=100"`
	Currency        string `json:"currency" validate:"required,len=3"`
	Note            string `json:"note,omitempty" validate:"max=500"`
}

type ListItemsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Role    string            `json:"-" validate:"omitempty,oneof=buyer seller"`
	Pending bool              `json:"-"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type ItemRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Item]    `json:"-" validate:"required"`
}

type CreateOfferRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	VariantID id.ID[id.Variant] `json:"variant_id" validate:"required"`
	Quantity  int64             `json:"quantity" validate:"required,gt=0"`
	Total     int64             `json:"total" validate:"required,gt=0"`
	Reason    string            `json:"reason,omitempty" validate:"max=500"`
}

type ListOffersRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"omitempty,oneof=active accepted checked-out cancelled"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type OfferRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Offer]   `json:"-" validate:"required"`
}

type CounterOfferRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	ID       id.ID[id.Offer]   `json:"-" validate:"required"`
	Quantity int64             `json:"quantity" validate:"required,gt=0"`
	Total    int64             `json:"total" validate:"required,gt=0"`
	Reason   string            `json:"reason,omitempty" validate:"max=500"`
}

// ShippingQuotesRequest asks what delivery would cost, so the buyer sees the fee before they
// commit to anything. Exactly one source, because the parcel has to be one purchase:
//
//   - VariantID — an estimate for a listing page, before any draft exists. Delivery is priced from
//     the *variant's* package details, not the listing's: one listing can hold an 80 g charger and
//     a 2 kg one, and they do not cost the same to send.
//   - DraftID — the frozen terms of a fixed-price sale, with Lines as the checkout would send them.
//   - OfferID — agreed terms, whose quantity was negotiated.
type ShippingQuotesRequest struct {
	ActorID   id.ID[id.Account]    `json:"-" validate:"required"`
	VariantID id.ID[id.Variant]    `json:"variant_id,omitempty"`
	DraftID   id.ID[id.DraftOrder] `json:"draft_id,omitempty"`
	OfferID   id.ID[id.Offer]      `json:"offer_id,omitempty"`
	// Quantity is how many of VariantID. Zero means one, since a listing page is quoting the
	// single unit a buyer is looking at. Ignored by the other two sources, which carry their own.
	Quantity int64 `json:"quantity,omitempty" validate:"omitempty,gt=0"`
	// ContactID is where the parcel goes. Optional: with none, the caller's default delivery
	// address is used, which is what lets a listing page quote without a form.
	ContactID id.ID[id.Contact] `json:"contact_id,omitempty"`
	// Lines are the draft's variants and quantities, as a checkout would send them. Ignored for
	// an offer, whose quantity is the negotiated one.
	Lines []CheckoutLine `json:"lines,omitempty" validate:"omitempty,max=50,dive"`
}

// ShippingQuotes is one entry per carrier that could price the parcel. A carrier that declined is
// simply absent — the buyer picks from whoever answered.
type ShippingQuotes struct {
	Options  []ShippingQuote `json:"options"`
	Currency string          `json:"currency"`
	// ContactID is the address these fees are for, echoed because the request may not have named
	// one: a fee with no address beside it is not a fee, and the client has to be able to show
	// which one it quoted and offer to change it.
	ContactID id.ID[id.Contact] `json:"contact_id"`
}

type ShippingQuote struct {
	Option string `json:"option"`
	Name   string `json:"name"`
	// Fee is what the buyer would pay this carrier for delivery. Re-quoted at checkout, so this
	// is an estimate a client renders rather than a price it can hold.
	Fee int64 `json:"fee"`
}

// CheckoutOfferRequest is the buyer's "create order now" on agreed terms: the address and the
// carrier, exactly as a fixed-price checkout takes them. The price is the offer's — only delivery
// is decided here, and the buyer pays it on both kinds of sale.
type CheckoutOfferRequest struct {
	ActorID         id.ID[id.Account] `json:"-" validate:"required"`
	ID              id.ID[id.Offer]   `json:"-" validate:"required"`
	ContactID       id.ID[id.Contact] `json:"contact_id" validate:"required"`
	TransportOption string            `json:"transport_option" validate:"required,max=100"`
	Note            string            `json:"note,omitempty" validate:"max=500"`
}

type ListOrdersRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Role    string            `json:"-" validate:"required,oneof=buyer seller"`
	State   string            `json:"-" validate:"omitempty,oneof=open completed cancelled"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type OrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
}

type ConfirmReceiptRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
	// At least one is mandatory: a later refund or dispute is judged on this evidence.
	Attachments []id.ID[id.Resource] `json:"attachments" validate:"required,min=1,max=10"`
}

// CancelOrderRequest carries nothing but who and which. It had a `reason` the service validated
// and dropped: there is no column for it, and a field a client fills in that reaches nobody is
// worse than no field.
type CancelOrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
}

// AdvanceShipmentRequest is a carrier checkpoint on the outbound leg, reported by the seller or
// corrected by a moderator. Forward-only, which is what makes "has this shipped" a fact a later
// report cannot undo.
type AdvanceShipmentRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
	Status  string            `json:"status" validate:"required,oneof=picked-up in-transit delivered returned failed"`
}

// AdvanceReturnShipmentRequest is the same for the leg carrying the goods back. `delivered` is
// what opens the seller's inspection window, so either party may report it — a seller who never
// confirms would otherwise strand the escrow, and round two is their remedy against a buyer who
// claims a delivery that did not happen.
type AdvanceReturnShipmentRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Refund]  `json:"-" validate:"required"`
	Status  string            `json:"status" validate:"required,oneof=picked-up in-transit delivered returned failed"`
}

type CreateRefundRequest struct {
	ActorID     id.ID[id.Account]    `json:"-" validate:"required"`
	OrderID     id.ID[id.Order]      `json:"-" validate:"required"`
	Reason      string               `json:"reason" validate:"required,min=1,max=2000"`
	Attachments []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
}

type ListRefundsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Role    string            `json:"-" validate:"omitempty,oneof=buyer seller"`
	Status  string            `json:"-"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type RefundRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Refund]  `json:"-" validate:"required"`
}

type AddRefundAttachmentsRequest struct {
	ActorID     id.ID[id.Account]    `json:"-" validate:"required"`
	ID          id.ID[id.Refund]     `json:"-" validate:"required"`
	Attachments []id.ID[id.Resource] `json:"attachments" validate:"required,min=1,max=10"`
}

type RejectRefundRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Refund]  `json:"-" validate:"required"`
	Reason  string            `json:"reason" validate:"required,min=1,max=2000"`
}

type OpenDisputeRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Refund]  `json:"-" validate:"required"`
	Reason  string            `json:"reason" validate:"required,min=1,max=2000"`
}

type ListDisputesRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type RuleDisputeRequest struct {
	ActorID   id.ID[id.Account]       `json:"-" validate:"required"`
	ID        id.ID[id.RefundDispute] `json:"-" validate:"required"`
	BuyerWins bool                    `json:"buyer_wins"`
	Note      string                  `json:"note,omitempty" validate:"max=2000"`
}

// CreateUploadRequest asks for a slot to PUT evidence into — the unboxing photos a buyer
// attaches confirming receipt, or the photos on a refund. The bytes never pass through the
// API: the answer is a short-lived signed URL, and a second call confirms the row once the
// object is there — so a dispute can never be judged on a photo whose bytes never arrived.
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

// ConfirmUploadRequest is the second step. The size is read from the store rather than taken
// from the client, so what it declared cannot become the record.
type ConfirmUploadRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.Resource] `json:"-" validate:"required"`
}

type Service interface {
	// --- cart ---
	ListCartItems(ctx context.Context, req ListCartRequest) ([]CartItem, error)
	AddCartItem(ctx context.Context, req AddCartItemRequest) (CartItem, error)
	UpdateCartItem(ctx context.Context, req UpdateCartItemRequest) (CartItem, error)
	DeleteCartItem(ctx context.Context, req CartItemRequest) error

	// --- purchase sessions ---
	CreateDraft(ctx context.Context, req CreateDraftRequest) (Draft, error)
	ListDrafts(ctx context.Context, req ListDraftsRequest) (DraftPage, error)
	GetDraft(ctx context.Context, req DraftRequest) (Draft, error)
	CancelDraft(ctx context.Context, req DraftRequest) error
	// ShippingQuotes prices every carrier for a variant, a draft or agreed terms, which is how a
	// buyer sees delivery before they pay for it. They pay it on both kinds of sale.
	ShippingQuotes(ctx context.Context, req ShippingQuotesRequest) (ShippingQuotes, error)
	// Checkout writes the lines and opens the payment session. The order follows when that
	// session completes — there is no route for it.
	Checkout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)

	// --- lines ---
	ListItems(ctx context.Context, req ListItemsRequest) (ItemPage, error)
	CancelItem(ctx context.Context, req ItemRequest) (Item, error)

	// --- negotiations ---
	CreateOffer(ctx context.Context, req CreateOfferRequest) (Offer, error)
	ListOffers(ctx context.Context, req ListOffersRequest) (OfferPage, error)
	GetOffer(ctx context.Context, req OfferRequest) (Offer, error)
	CounterOffer(ctx context.Context, req CounterOfferRequest) (Offer, error)
	CancelOffer(ctx context.Context, req OfferRequest) error
	// AcceptOffer agrees to the terms on the table — whoever does not own the standing proposal,
	// so either side may. It charges nothing: it freezes the price and starts a short window.
	AcceptOffer(ctx context.Context, req OfferRequest) (Offer, error)
	// CheckoutOffer is the buyer's "create order now" on those agreed terms: they pick delivery
	// and pay, in the same checkout a fixed-price listing uses.
	CheckoutOffer(ctx context.Context, req CheckoutOfferRequest) (CheckoutResult, error)

	// --- orders ---
	ListOrders(ctx context.Context, req ListOrdersRequest) (OrderPage, error)
	GetOrder(ctx context.Context, req OrderRequest) (Order, error)
	ConfirmReceipt(ctx context.Context, req ConfirmReceiptRequest) (Order, error)
	CancelOrder(ctx context.Context, req CancelOrderRequest) (Order, error)
	GetOrderTransport(ctx context.Context, req OrderRequest) (Transport, error)
	// AdvanceShipment records a carrier checkpoint on the outbound leg. The seller's route:
	// nothing else writes that status, and "has this shipped" is what decides whether an order
	// can still be cancelled and the escrow taken back.
	AdvanceShipment(ctx context.Context, req AdvanceShipmentRequest) (Transport, error)

	// --- refunds and disputes ---
	CreateRefund(ctx context.Context, req CreateRefundRequest) (Refund, error)
	ListRefunds(ctx context.Context, req ListRefundsRequest) (RefundPage, error)
	GetRefund(ctx context.Context, req RefundRequest) (Refund, error)
	WithdrawRefund(ctx context.Context, req RefundRequest) error
	AddRefundAttachments(ctx context.Context, req AddRefundAttachmentsRequest) (Refund, error)
	AcceptRefund(ctx context.Context, req RefundRequest) (Refund, error)
	RejectRefund(ctx context.Context, req RejectRefundRequest) (Refund, error)
	// AdvanceReturnShipment is the only exit from `returning`: marking the return delivered is
	// what opens the seller's appeal window, and without it a granted refund strands the escrow
	// with nobody on a clock.
	AdvanceReturnShipment(ctx context.Context, req AdvanceReturnShipmentRequest) (Refund, error)
	OpenDispute(ctx context.Context, req OpenDisputeRequest) (Dispute, error)
	AdminListDisputes(ctx context.Context, req ListDisputesRequest) (DisputePage, error)
	AdminRuleDispute(ctx context.Context, req RuleDisputeRequest) (Dispute, error)

	// --- uploads ---
	// CreateUpload reserves a slot for evidence: the unboxing photos a receipt confirmation
	// or a refund carries. The client PUTs the bytes at the store and confirms; until then
	// the resource resolves to nothing, so a half-finished upload cannot be named as evidence.
	CreateUpload(ctx context.Context, req CreateUploadRequest) (UploadSlot, error)
	ConfirmUpload(ctx context.Context, req ConfirmUploadRequest) (common.ResourceDTO, error)

	// --- driven by the durable workflow, not by a route ---
	//
	// Each is idempotent and safe to call again: Restate journals a step and retries it,
	// so a second call has to be a no-op rather than a second effect. That is what lets the
	// timers live in the workflow instead of in a cron table here.

	// SettlePaidSession turns a completed payment session into an order. Called by the
	// subscriber on finance's event and by the workflow that follows the payment.
	SettlePaidSession(ctx context.Context, sessionID id.ID[id.PaymentSession]) error
	// ExpireDrafts and ExpireOffers close what nobody finished; ExpireCheckouts gives back the
	// stock a checkout nobody paid for is holding, which nothing else in the schema looks at.
	ExpireDrafts(ctx context.Context, limit int) (int, error)
	ExpireCheckouts(ctx context.Context, limit int) (int, error)
	ExpireOffers(ctx context.Context, limit int) (int, error)
	// ReleaseDuePayouts pays out orders whose escrow window has passed with no live refund;
	// RetryClaimedPayouts is the second half for one that got as far as claiming the order and
	// then could not reach finance.
	ReleaseDuePayouts(ctx context.Context, limit int) (int, error)
	RetryClaimedPayouts(ctx context.Context, limit int) (int, error)
	// AdvanceOverdueRefunds moves every refund whose deadline has passed — all three
	// windows, because each non-terminal status names the party it waits on.
	AdvanceOverdueRefunds(ctx context.Context, limit int) (int, error)

	// The same two transitions for one entity, which is what a per-entity durable run calls.
	// A bulk pass with a limit of one acts on whichever row is oldest, so a single stuck head
	// starves every run there is.
	ReleasePayout(ctx context.Context, orderID id.ID[id.Order]) error
	AdvanceRefund(ctx context.Context, refundID id.ID[id.Refund]) error
}

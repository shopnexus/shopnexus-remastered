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
	PayoutDeadlineAt *time.Time                `json:"payout_deadline_at"`
	PayoutSessionID  *id.ID[id.PaymentSession] `json:"payout_session_id"`
	CreatedAt        time.Time                 `json:"created_at"`
	CompletedAt      *time.Time                `json:"completed_at"`
	CancelledAt      *time.Time                `json:"cancelled_at"`
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
	ID        id.ID[id.Transport] `json:"id"`
	Option    string              `json:"option"`
	Status    string              `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
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

// CheckoutResult is the lines and the single session that pays for them. Nothing is settled
// yet: the order appears when the session completes.
type CheckoutResult struct {
	Items          []Item                   `json:"items"`
	PaymentSession id.ID[id.PaymentSession] `json:"payment_session_id"`
	Total          int64                    `json:"total"`
	Currency       string                   `json:"currency"`
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
	Status  string            `json:"-" validate:"omitempty,oneof=active accepted cancelled"`
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

// AcceptOfferRequest still needs an address and a carrier: the negotiation settled price
// and quantity, and the buyer pays delivery either way.
type AcceptOfferRequest struct {
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
	Note        string               `json:"note,omitempty" validate:"max=2000"`
}

type CancelOrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
	Reason  string            `json:"reason,omitempty" validate:"max=500"`
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
	// AcceptOffer is the buyer closing it, which opens the same checkout a fixed-price
	// listing uses.
	AcceptOffer(ctx context.Context, req AcceptOfferRequest) (CheckoutResult, error)

	// --- orders ---
	ListOrders(ctx context.Context, req ListOrdersRequest) (OrderPage, error)
	GetOrder(ctx context.Context, req OrderRequest) (Order, error)
	ConfirmReceipt(ctx context.Context, req ConfirmReceiptRequest) (Order, error)
	CancelOrder(ctx context.Context, req CancelOrderRequest) (Order, error)
	GetOrderTransport(ctx context.Context, req OrderRequest) (Transport, error)

	// --- refunds and disputes ---
	CreateRefund(ctx context.Context, req CreateRefundRequest) (Refund, error)
	ListRefunds(ctx context.Context, req ListRefundsRequest) (RefundPage, error)
	GetRefund(ctx context.Context, req RefundRequest) (Refund, error)
	WithdrawRefund(ctx context.Context, req RefundRequest) error
	AddRefundAttachments(ctx context.Context, req AddRefundAttachmentsRequest) (Refund, error)
	AcceptRefund(ctx context.Context, req RefundRequest) (Refund, error)
	RejectRefund(ctx context.Context, req RejectRefundRequest) (Refund, error)
	OpenDispute(ctx context.Context, req OpenDisputeRequest) (Dispute, error)
	AdminListDisputes(ctx context.Context, req ListDisputesRequest) (DisputePage, error)
	AdminRuleDispute(ctx context.Context, req RuleDisputeRequest) (Dispute, error)

	// --- driven by the durable workflow, not by a route ---
	//
	// Each is idempotent and safe to call again: Restate journals a step and retries it,
	// so a second call has to be a no-op rather than a second effect. That is what lets the
	// timers live in the workflow instead of in a cron table here.

	// SettlePaidSession turns a completed payment session into an order. Called by the
	// subscriber on finance's event and by the workflow that follows the payment.
	SettlePaidSession(ctx context.Context, sessionID id.ID[id.PaymentSession]) error
	// ExpireDrafts and ExpireOffers close what nobody finished.
	ExpireDrafts(ctx context.Context, limit int) (int, error)
	ExpireOffers(ctx context.Context, limit int) (int, error)
	// ReleaseDuePayouts pays out orders whose escrow window has passed with no live refund.
	ReleaseDuePayouts(ctx context.Context, limit int) (int, error)
	// AdvanceOverdueRefunds moves every refund whose deadline has passed — all three
	// windows, because each non-terminal status names the party it waits on.
	AdvanceOverdueRefunds(ctx context.Context, limit int) (int, error)
}

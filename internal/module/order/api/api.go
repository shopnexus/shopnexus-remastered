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
	// StateAwaitingConfirmation is paid but not yet accepted by the seller. Nothing has been
	// handed to a carrier in this state, which is the whole reason it is a state.
	StateAwaitingConfirmation = "awaiting-confirmation"
	StateOpen                 = "open"
	StateCompleted            = "completed"
	StateCancelled            = "cancelled"
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
	Attributes map[string]any    `json:"attributes"`
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
	Note             string                   `json:"note"`
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
	AuthorID id.ID[id.Account] `json:"author_id"`
	// Listing is what is being haggled over, resolved rather than left as an id: a list of
	// offers carrying only ids renders as a column of prices with nothing to tell them apart,
	// which is the same rule that makes an attachment travel as a resolved ResourceDTO.
	Listing OfferListing `json:"listing"`
	// Counterparty is the other side, always — `/offers` only ever answers a party to the
	// row, so the viewer is one of BuyerID/SellerID and the one they need named is the other.
	Counterparty accountapi.AccountSummary `json:"counterparty"`
	Status       string                    `json:"status"`
	Quantity     int64                     `json:"quantity"`
	Total        int64                     `json:"total"`
	Currency     string                    `json:"currency"`
	Reason       string                    `json:"reason"`
	CreatedAt    time.Time                 `json:"created_at"`
	ExpiresAt    time.Time                 `json:"expires_at"`
}

// OfferListing is the little of a listing an offer row has to show. Read live rather than
// snapshotted: a renamed listing should read as its current name here, and the terms — which
// are the part that must not drift — are on the offer itself.
type OfferListing struct {
	Name  string              `json:"name"`
	Cover *common.ResourceDTO `json:"cover"`
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
	Transport     *Transport                `json:"transport"`
	// ConfirmedAt is the seller accepting the sale. Null means the parcel has not been booked
	// and will not be: a buyer reading this knows what they are waiting on.
	ConfirmedAt *time.Time `json:"confirmed_at"`
	// ConfirmationDeadlineAt is when the seller runs out of time to accept, after which staff
	// are asked to chase it. Null once they have, and computed rather than stored.
	ConfirmationDeadlineAt *time.Time `json:"confirmation_deadline_at"`
	// DeclineReason is why the seller refused, set only on an order they refused outright.
	DeclineReason *string    `json:"decline_reason"`
	ReceivedAt    *time.Time `json:"received_at"`
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

// OrderRef names an order without its contents. It is what a realtime event carries: the
// two sides of a sale see different projections of an order, so pushing the entity would
// mean deciding whose view to send — the id lets each client fetch its own.
type OrderRef struct {
	ID id.ID[id.Order] `json:"id"`
}

type AddressSnapshot struct {
	FullName      string  `json:"full_name"`
	Phone         string  `json:"phone"`
	Country       string  `json:"country"`
	ProvinceCode  string  `json:"province_code"`
	DistrictCode  *string `json:"district_code"`
	WardCode      string  `json:"ward_code"`
	AddressDetail *string `json:"address_detail"`
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
	// SellerDecidedAt is when the seller answered — by granting it, or by handing it to staff.
	// There is no rejection reason beside it: a seller cannot refuse a refund on their own word.
	SellerDecidedAt *time.Time `json:"seller_decided_at"`
	ReturnedAt      *time.Time `json:"returned_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// OrderCase is a sale and the one unsettled refund on it, if there is one. Two reads that a
// decision always needs together — the verdict route names the refund, while every ticket about
// a sale names the order.
type OrderCase struct {
	Order Order `json:"order"`
	// Null when nothing is being disputed.
	Refund *Refund `json:"refund"`
}

type RefundPage struct {
	Data []Refund   `json:"data"`
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
	NextCursor string `json:"next_cursor"`
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
	Note            string `json:"note" validate:"max=500"`
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
	Reason    string            `json:"reason" validate:"max=500"`
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
	Reason   string            `json:"reason" validate:"max=500"`
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
	VariantID id.ID[id.Variant]    `json:"variant_id"`
	DraftID   id.ID[id.DraftOrder] `json:"draft_id"`
	OfferID   id.ID[id.Offer]      `json:"offer_id"`
	// Quantity is how many of VariantID. Zero means one, since a listing page is quoting the
	// single unit a buyer is looking at. Ignored by the other two sources, which carry their own.
	Quantity int64 `json:"quantity" validate:"omitempty,gt=0"`
	// ContactID is where the parcel goes. Optional: with none, the caller's default delivery
	// address is used, which is what lets a listing page quote without a form.
	ContactID id.ID[id.Contact] `json:"contact_id"`
	// Lines are the draft's variants and quantities, as a checkout would send them. Ignored for
	// an offer, whose quantity is the negotiated one.
	Lines []CheckoutLine `json:"lines" validate:"omitempty,max=50,dive"`
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
	Note            string            `json:"note" validate:"max=500"`
}

// OrderSummaryRequest is the window a dashboard reads. The window filters `created_at`, so every
// number in the answer describes the same set — the orders placed in it, as they stand now.
type OrderSummaryRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Role    string            `json:"-" validate:"required,oneof=buyer seller"`
	// From and To bound the window; absent means the last 30 days ending now. To is exclusive.
	From *time.Time `json:"-"`
	To   *time.Time `json:"-"`
	// TZ is the IANA zone the daily buckets are cut on. A seller in Asia/Ho_Chi_Minh reading UTC
	// buckets sees yesterday's evening sales land on today. Empty means UTC.
	TZ string `json:"-" validate:"omitempty,max=64"`
}

// MoneyByCurrency is one currency's worth of a total. A list rather than a single number because a
// shop may price in more than one currency, and adding those together is a figure that means nothing.
type MoneyByCurrency struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// OrderSummaryDay is one bucket of the series behind a chart: counts only. Money is summarised for
// the whole window instead, for the reason MoneyByCurrency gives.
type OrderSummaryDay struct {
	// Date is `YYYY-MM-DD` in the requested zone, not a timestamp: a bucket is a day, and a midnight
	// instant would invite a reader to convert it into a different one.
	Date      string `json:"date"`
	Placed    int64  `json:"placed"`
	Completed int64  `json:"completed"`
}

// OrderSummary is what a seller's dashboard shows: how the window's orders stand, and what the
// finished ones came to.
type OrderSummary struct {
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Open      int64     `json:"open"`
	Completed int64     `json:"completed"`
	Cancelled int64     `json:"cancelled"`
	// Totals counts the goods of completed orders only, and excludes a cancelled line and the
	// delivery fee — the fee is the courier's money and never reaches the seller.
	Totals []MoneyByCurrency `json:"totals"`
	Daily  []OrderSummaryDay `json:"daily"`
}

type ListOrdersRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	// Role is optional, and absent means both sides. A C2C account is a buyer and a seller
	// at once, so "what is waiting on me" spans the two — demanding a side made every client
	// grow a buyer/seller switch, and the more urgent of the two was then behind a tap.
	Role    string            `json:"-" validate:"omitempty,oneof=buyer seller"`
	State   string            `json:"-" validate:"omitempty,oneof=awaiting-confirmation open completed cancelled"`
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
	// At least one is mandatory: a later refund is judged on this evidence.
	Attachments []id.ID[id.Resource] `json:"attachments" validate:"required,min=1,max=10"`
}

// CancelOrderRequest carries nothing but who and which. It had a `reason` the service validated
// and dropped: there is no column for it, and a field a client fills in that reaches nobody is
// worse than no field.
type CancelOrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
}

// AdvanceShipmentRequest is a moderator correcting a checkpoint on the outbound leg. The carrier
// reports the leg itself; neither party to the order writes it. Forward-only, which is what makes
// "has this shipped" a fact a later report cannot undo.
type AdvanceShipmentRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
	Status  string            `json:"status" validate:"required,oneof=picked-up in-transit delivered returned failed"`
}

// AdvanceReturnShipmentRequest is the same for the leg carrying the goods back, which no carrier
// reports on: that leg is never booked with one, so both parties may write it. Who reports
// `delivered` is what decides where the case goes — the seller acknowledging receipt opens their
// own inspection window, while the buyer's claim goes to staff, because a window that pays out on
// the seller's silence is one a buyer who posted nothing can simply wait out.
type AdvanceReturnShipmentRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Refund]  `json:"-" validate:"required"`
	Status  string            `json:"status" validate:"required,oneof=picked-up in-transit delivered returned failed"`
}

type CreateRefundRequest struct {
	ActorID     id.ID[id.Account]    `json:"-" validate:"required"`
	OrderID     id.ID[id.Order]      `json:"-" validate:"required"`
	Reason      string               `json:"reason" validate:"required,min=1,max=2000"`
	Attachments []id.ID[id.Resource] `json:"attachments" validate:"max=10"`
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

// ConfirmOrderRequest is the seller accepting a paid sale, which is what books the parcel.
type ConfirmOrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
}

// DeclineOrderRequest is the seller refusing one. The reason is required and it is kept: the
// cancellation says only that the sale did not happen, where this says who ended it and why.
type DeclineOrderRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Order]   `json:"-" validate:"required"`
	Reason  string            `json:"reason" validate:"required,min=1,max=500"`
}

// EscalateRefundRequest has no reason field: trust's ticket carries what the escalating party
// said, so a second copy here could disagree with it.
// EscalateRefundRequest names the *order*, not the refund. Trust opens its ticket against the sale
// so every dispute about it groups into one thread, and one live refund per order is an index — so
// which case that is stays order's to resolve rather than a second id the caller has to carry.
type EscalateRefundRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	OrderID id.ID[id.Order]   `json:"-" validate:"required"`
}

// ResolveRefundRequest is the verdict. A boolean rather than a status, because there is no
// "still deciding" outcome to record — and the Note travels on the published fact, which is
// what closes the ticket the escalation opened.
type ResolveRefundRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	ID        id.ID[id.Refund]  `json:"-" validate:"required"`
	BuyerWins bool              `json:"buyer_wins"`
	Note      string            `json:"note" validate:"max=2000"`
}

// CreateUploadRequest asks for a slot to PUT evidence into — the unboxing photos a buyer
// attaches confirming receipt, or the photos on a refund. The bytes never pass through the
// API: the answer is a short-lived signed URL, and a second call confirms the row once the
// object is there — so a refund can never be judged on a photo whose bytes never arrived.
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
	Headers   map[string]string `json:"headers"`
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
	// ListOptions is the carriers, and AdminSaveOption the operator's edit of one. Both are the
	// shared registry surface (`GET /options?category=transport`), served by this module because
	// the rows live in its schema.
	ListOptions(ctx context.Context, req common.ListOptionsRequest) (common.OptionList, error)
	AdminSaveOption(ctx context.Context, req common.SaveOptionRequest) (common.OptionDTO, error)
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
	// GetOrderSummary is the caller's own sales (or purchases) over a window: the counts a dashboard
	// leads with, and the goods money of the completed ones.
	GetOrderSummary(ctx context.Context, req OrderSummaryRequest) (OrderSummary, error)
	GetOrder(ctx context.Context, req OrderRequest) (Order, error)
	// ConfirmOrder and DeclineOrder are the seller's answer to a paid sale. Nothing reaches a
	// carrier before the first, and the second is a cancellation that records why.
	ConfirmOrder(ctx context.Context, req ConfirmOrderRequest) (Order, error)
	DeclineOrder(ctx context.Context, req DeclineOrderRequest) (Order, error)
	ConfirmReceipt(ctx context.Context, req ConfirmReceiptRequest) (Order, error)
	CancelOrder(ctx context.Context, req CancelOrderRequest) (Order, error)
	GetOrderTransport(ctx context.Context, req OrderRequest) (Transport, error)
	// AdvanceShipment corrects a checkpoint on the outbound leg. Staff only — the carrier's
	// webhook is where that status comes from — because "has this shipped" decides whether an
	// order can still be cancelled and the escrow taken back, so it is not a party's to claim.
	AdvanceShipment(ctx context.Context, req AdvanceShipmentRequest) (Transport, error)

	// --- refunds ---
	CreateRefund(ctx context.Context, req CreateRefundRequest) (Refund, error)
	ListRefunds(ctx context.Context, req ListRefundsRequest) (RefundPage, error)
	GetRefund(ctx context.Context, req RefundRequest) (Refund, error)
	WithdrawRefund(ctx context.Context, req RefundRequest) error
	AddRefundAttachments(ctx context.Context, req AddRefundAttachmentsRequest) (Refund, error)
	AcceptRefund(ctx context.Context, req RefundRequest) (Refund, error)
	// AdvanceReturnShipment is the only exit from `returning`, since nothing books that leg with a
	// carrier. The seller reporting it delivered opens their inspection window; the buyer reporting
	// it hands the case to staff, because their own word must not settle it in their favour.
	AdvanceReturnShipment(ctx context.Context, req AdvanceReturnShipmentRequest) (Refund, error)
	// EscalateRefund records that staff have been asked to decide. Called by trust when the
	// ticket is opened — not by a route — so a refund's status and its ticket cannot disagree.
	EscalateRefund(ctx context.Context, req EscalateRefundRequest) (Refund, error)
	// GetOrderCase is an order together with the live refund on it, for whoever has to decide
	// something about it. Staff are not a party to a sale, so every ordinary order and refund
	// read 404s them — which left AdminResolveRefund taking a refund id that nothing
	// staff-facing could produce, and a refund dispute with no way to be decided.
	GetOrderCase(ctx context.Context, req OrderRequest) (OrderCase, error)
	// AdminResolveRefund is the verdict, and the only thing staff decide here: order owns the
	// money, so it owns the outcome the money follows.
	AdminResolveRefund(ctx context.Context, req ResolveRefundRequest) (Refund, error)

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

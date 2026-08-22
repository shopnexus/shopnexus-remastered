package domain

import (
	"maps"
	"slices"
)

// Kind names one fact this platform tells an account about, and it is the whole identity of
// a notification: the category it files under, the words it renders as, the page it links to
// and the letter that carries it are all looked up from it.
//
// It replaces a `title` column plus a separate `mail_kind` argument, which were two names for
// one fact and could disagree — a feed row saying "Order placed" beside a cancellation mail.
// Storing the kind and the facts instead means the words are chosen when the notification is
// *read*, in the language of whoever is reading, and changing copy is editing a template
// rather than migrating rows nobody can retranslate.
type Kind string

// A kind carries its audience wherever buyer and seller read different words or land on a
// different page — which, on a marketplace, is most of them. `sale-*` is the seller's side of
// a fact the buyer knows as `order-*`.
const (
	// The sale, as the buyer follows it.
	KindOrderPlaced      Kind = "order-placed"
	KindOrderDelivered   Kind = "order-delivered"
	KindOrderCompleted   Kind = "order-completed"
	KindOrderCancelled   Kind = "order-cancelled"
	KindOrderUnconfirmed Kind = "order-unconfirmed"
	KindRefundResolved   Kind = "refund-resolved"
	// KindRefundEscalated is staff picking up a case the buyer never had to chase. There is no
	// letter: nothing is decided yet, and a mail about a case still being read is a mail that
	// makes somebody check their inbox for the next one.
	KindRefundEscalated Kind = "refund-escalated"

	// The same sale, as the seller sees it.
	KindSaleReceived       Kind = "sale-received"
	KindSaleHandedOver     Kind = "sale-handed-over"
	KindSaleCompleted      Kind = "sale-completed"
	KindSaleCancelled      Kind = "sale-cancelled"
	KindSaleUnconfirmed    Kind = "sale-unconfirmed"
	KindSaleRefundResolved Kind = "sale-refund-resolved"

	// A negotiation's standing terms changing. Whoever did not cause it is told; the thread
	// they were already talking in is where it happened, so that is where the link goes.
	KindOfferCountered Kind = "offer-countered"
	KindOfferAccepted  Kind = "offer-accepted"
	KindOfferWithdrawn Kind = "offer-withdrawn"

	// What the platform did to the account's own things. There is no payout kind: the only
	// money that leaves this platform on somebody's instruction is a withdrawal, and a seller's
	// escrow release is reported as the sale completing rather than as a second movement.
	KindListingApproved  Kind = "listing-approved"
	KindListingTakenDown Kind = "listing-taken-down"
	KindWithdrawalPaid   Kind = "withdrawal-paid"
	KindPayoutFailed     Kind = "payout-failed"
	KindCheckoutExpired  Kind = "checkout-expired"

	// The follow graph.
	KindNewFollower Kind = "new-follower"
)

// KindSpec is everything that follows from a kind: nothing here is stored on the row, so all
// of it changes without a migration.
type KindSpec struct {
	// Category files the notification, and is the preference key that decides whether it is
	// delivered at all.
	Category Category
	// Mail is the template in templates/mail that carries this fact by letter, empty where the
	// fact is worth a feed row and not an envelope.
	//
	// A plain string because a domain does not import a provider; TestKindSpecs_MailTemplates
	// asserts every one of these is a kind notify actually has copy for, so a typo here is a
	// failing test rather than a mail that fails at 3am.
	Mail string
	// Href is the page the notification opens, built from the payload. Structure, not copy, so
	// it lives here and not in the copybook — a link must not vary by language.
	//
	// Empty when the payload is missing what the link needs, which renders as a row with
	// nowhere to go rather than a link to a 404.
	Href func(payload map[string]any) string
}

// kindSpecs is the whole vocabulary. A kind absent from this map does not exist: NewNotification
// refuses it, so an emitter's typo cannot become a feed row nobody has words for.
var kindSpecs = map[Kind]KindSpec{
	KindOrderPlaced:      {Category: CategoryOrder, Mail: "order-placed", Href: orderHref},
	KindOrderDelivered:   {Category: CategoryOrder, Mail: "order-delivered", Href: orderHref},
	KindOrderCompleted:   {Category: CategoryOrder, Mail: "order-completed", Href: orderHref},
	KindOrderCancelled:   {Category: CategoryOrder, Mail: "order-cancelled", Href: orderHref},
	KindOrderUnconfirmed: {Category: CategoryOrder, Mail: "order-unconfirmed", Href: orderHref},
	KindRefundResolved:   {Category: CategoryOrder, Mail: "refund-resolved", Href: refundHref},
	KindRefundEscalated:  {Category: CategoryOrder, Href: refundHref},

	// The seller's side shares the buyer's letter wherever one exists — the words differ, the
	// fact does not — and links to their own sales list rather than the buyer's order page.
	KindSaleReceived:       {Category: CategoryOrder, Mail: "order-received", Href: salesHref},
	KindSaleHandedOver:     {Category: CategoryOrder, Href: salesHref},
	KindSaleCompleted:      {Category: CategoryOrder, Mail: "order-completed", Href: salesHref},
	KindSaleCancelled:      {Category: CategoryOrder, Mail: "order-cancelled", Href: salesHref},
	KindSaleUnconfirmed:    {Category: CategoryOrder, Href: salesHref},
	KindSaleRefundResolved: {Category: CategoryOrder, Mail: "refund-resolved", Href: refundHref},

	KindOfferCountered: {Category: CategoryOrder, Href: staticHref("/inbox")},
	KindOfferAccepted:  {Category: CategoryOrder, Href: staticHref("/inbox")},
	KindOfferWithdrawn: {Category: CategoryOrder, Href: staticHref("/inbox")},

	KindListingApproved:  {Category: CategorySystem, Href: listingHref},
	KindListingTakenDown: {Category: CategorySystem, Href: listingHref},
	KindWithdrawalPaid:   {Category: CategorySystem, Href: staticHref("/account/wallet")},
	KindPayoutFailed:     {Category: CategorySystem, Href: staticHref("/account/wallet")},
	KindCheckoutExpired:  {Category: CategorySystem, Href: staticHref("/cart")},

	KindNewFollower: {Category: CategorySocial, Href: followerHref},
}

// Kinds is every kind, in a stable order, for the tests and the loaders that have to demand
// copy for all of them. Derived from the map and sorted, so adding a kind above is the only
// edit — a second hand-kept list is a list that drifts.
var Kinds = slices.Sorted(maps.Keys(kindSpecs))

// SpecOf answers what follows from a kind, and false for one this domain does not know.
func SpecOf(k Kind) (KindSpec, bool) {
	spec, ok := kindSpecs[k]
	return spec, ok
}

// The link builders. Each names the payload key it reads, which is the contract an emitter
// keeps and templates/notification/*.yaml renders from the same map.
func orderHref(p map[string]any) string    { return ref(p, "order_id", "/account/orders/") }
func listingHref(p map[string]any) string  { return ref(p, "listing_id", "/account/products/") }
func followerHref(p map[string]any) string { return ref(p, "follower_id", "/shop/") }

// A refund and a sale have no page of their own per row: both parties read the list.
func refundHref(map[string]any) string { return "/account/refunds" }
func salesHref(map[string]any) string  { return "/account/sales" }

func staticHref(path string) func(map[string]any) string {
	return func(map[string]any) string { return path }
}

// ref builds a link from an opaque id in the payload. Opaque because that is what the emitter
// put there — the same string the recipient sees everywhere else — so nothing here encodes.
func ref(p map[string]any, key, prefix string) string {
	s, ok := p[key].(string)
	if !ok || s == "" {
		return ""
	}
	return prefix + s
}

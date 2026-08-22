package account

import (
	"context"
	"fmt"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/finance"
	financedomain "shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/order"
	"shopnexus/internal/shared/id"
)

// The subscribers that turn another module's facts into this one's feed rows.
//
// One notification per interested party, because a notification belongs to an account: a sale
// has two sides and there is no shared inbox. A handler is not idempotent and does not need to
// be — a redelivered event costs a duplicate feed row, which is a cosmetic fault, and
// de-duplicating it would need a uniqueness rule the feed does not have.
//
// What a caller sends is a *kind* and the facts behind it. The words, the link and the mail
// template are all looked up from the kind when the row is read, so nothing in this file
// contains a sentence — which is the point: it used to hold English titles that reached
// Vietnamese readers, and a mail template named beside them that could disagree.

// SubscribeOrderEvents turns the sale's facts into feed rows for both of its sides.
func SubscribeOrderEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderPlacedTopic, "account", func(ctx context.Context, e order.OrderPlaced) error {
		// The sale's terms, which the buyer's copy, the seller's copy and both mails render
		// from the one map.
		payload := map[string]any{
			"order_id": orderRef(e.OrderID),
			"total":    e.Total,
			"currency": e.Currency,
		}
		return notifyParties(ctx, svc, log, notice{
			payload: payload,
			to: []party{
				{accountID: e.BuyerID, kind: domain.KindOrderPlaced},
				{accountID: e.SellerID, kind: domain.KindSaleReceived},
			},
		})
	})

	eventbus.Subscribe(bus, order.OrderDeliveredTopic, "account", func(ctx context.Context, e order.OrderDelivered) error {
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{"order_id": orderRef(e.OrderID)},
			// Confirming receipt is the buyer's to do and it is what releases the escrow, so
			// their row asks for it; the seller's reports that the parcel landed.
			to: []party{
				{accountID: e.BuyerID, kind: domain.KindOrderDelivered},
				{accountID: e.SellerID, kind: domain.KindSaleHandedOver},
			},
		})
	})

	eventbus.Subscribe(bus, order.OrderSettledTopic, "account", func(ctx context.Context, e order.OrderSettled) error {
		buyer, seller := domain.KindOrderCompleted, domain.KindSaleCompleted
		if !e.Completed {
			buyer, seller = domain.KindOrderCancelled, domain.KindSaleCancelled
		}
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{"order_id": orderRef(e.OrderID)},
			to: []party{
				{accountID: e.BuyerID, kind: buyer},
				{accountID: e.SellerID, kind: seller},
			},
		})
	})

	eventbus.Subscribe(bus, order.RefundResolvedTopic, "account", func(ctx context.Context, e order.RefundResolved) error {
		return notifyParties(ctx, svc, log, notice{
			// The verdict and the note travel as the moderator left them: the copy states which
			// way it went and quotes the reasoning, and adds nothing of its own to either.
			payload: map[string]any{
				"order_id":   orderRef(e.OrderID),
				"buyer_wins": e.BuyerWins,
				"note":       e.Note,
			},
			to: []party{
				{accountID: e.BuyerID, kind: domain.KindRefundResolved},
				{accountID: e.SellerID, kind: domain.KindSaleRefundResolved},
			},
		})
	})

	eventbus.Subscribe(bus, order.RefundEscalatedTopic, "account", func(ctx context.Context, e order.RefundEscalated) error {
		// The buyer alone. A seller learns a case went to staff from the case itself, and this
		// row exists to stop the buyer chasing a seller who has stopped answering.
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{"order_id": orderRef(e.OrderID)},
			to:      []party{{accountID: e.BuyerID, kind: domain.KindRefundEscalated}},
		})
	})

	eventbus.Subscribe(bus, order.OrderConfirmationLapsedTopic, "account", func(ctx context.Context, e order.OrderConfirmationLapsed) error {
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{"order_id": orderRef(e.OrderID)},
			// Only the buyer is mailed, by the kind's own spec: chasing a seller who did not act
			// is support's job, and a letter from here would be this platform apologising on
			// their behalf. They still get the feed row, which is a nudge and not a letter.
			to: []party{
				{accountID: e.BuyerID, kind: domain.KindOrderUnconfirmed},
				{accountID: e.SellerID, kind: domain.KindSaleUnconfirmed},
			},
		})
	})

	eventbus.Subscribe(bus, order.OfferChangedTopic, "account", func(ctx context.Context, e order.OfferChanged) error {
		kind, ok := offerKind(e.Change)
		if !ok {
			// A transition this module has no words for is not an error: order may report one
			// before there is copy, and nacking would redeliver it for ever.
			return nil
		}
		// Whoever did not cause it. The actor is looking at the thread they just typed in.
		recipient := e.BuyerID
		if e.ActorID == e.BuyerID {
			recipient = e.SellerID
		}
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{
				"listing_name": e.ListingName,
				"price":        e.Total,
				"currency":     e.Currency,
			},
			to: []party{{accountID: recipient, kind: kind}},
		})
	})
}

// SubscribeCatalogEvents tells a seller what a moderator decided about their listing.
func SubscribeCatalogEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, catalog.ListingModeratedTopic, "account", func(ctx context.Context, e catalog.ListingModerated) error {
		kind := domain.KindListingTakenDown
		if e.Approved {
			kind = domain.KindListingApproved
		}
		// The moderator's choice, honoured here rather than in the publisher: whether a seller
		// hears about a takedown is a product rule, and catalog carries the flag without
		// deciding it. An approval is always told — a seller waiting on a queue is the one
		// person who asked to be.
		if !e.Approved && !e.NotifySeller {
			return nil
		}
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{
				"listing_id":   listingRef(e.ListingID),
				"listing_name": e.Name,
				"reason":       e.Reason,
			},
			to: []party{{accountID: e.SellerID, kind: kind}},
		})
	})
}

// SubscribeFinanceEvents tells an account what happened to money it moved itself.
//
// A paid buyer-checkout is deliberately silent: it becomes an order, and `order.placed` already
// says so. Two rows for one act is how a feed becomes something people mute.
func SubscribeFinanceEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, finance.SessionPaidTopic, "account", func(ctx context.Context, e finance.SessionPaid) error {
		if e.Kind != financedomain.KindWithdrawal {
			return nil
		}
		return notifyParties(ctx, svc, log, notice{
			payload: map[string]any{"amount": e.Amount, "currency": e.Currency},
			// FromID: a withdrawal debits the wallet of whoever asked for it, and has no payee
			// inside this platform.
			to: []party{{accountID: e.FromID, kind: domain.KindWithdrawalPaid}},
		})
	})

	eventbus.Subscribe(bus, finance.SessionCancelledTopic, "account", func(ctx context.Context, e finance.SessionCancelled) error {
		kind, ok := cancelledSessionKind(e.Kind)
		if !ok {
			return nil
		}
		// No amount on this event, and the copy asks for none: what a reader needs is that the
		// money is back and what to do next, not a figure they can read in their wallet.
		return notifyParties(ctx, svc, log, notice{to: []party{{accountID: e.FromID, kind: kind}}})
	})
}

func offerKind(change string) (domain.Kind, bool) {
	switch change {
	case order.OfferChangeCountered:
		return domain.KindOfferCountered, true
	case order.OfferChangeAccepted:
		return domain.KindOfferAccepted, true
	case order.OfferChangeWithdrawn:
		return domain.KindOfferWithdrawn, true
	default:
		return "", false
	}
}

func cancelledSessionKind(sessionKind string) (domain.Kind, bool) {
	switch sessionKind {
	case financedomain.KindBuyerCheckout:
		return domain.KindCheckoutExpired, true
	case financedomain.KindWithdrawal:
		return domain.KindPayoutFailed, true
	default:
		return "", false
	}
}

// notice is one fact told to the accounts it concerns. A struct rather than positional
// arguments, because a payload and a recipient list transpose without a compile error.
type notice struct {
	payload map[string]any
	to      []party
}

// party is one recipient and the kind they are told. The kind differs between the two sides of
// a sale wherever they read different words or land on a different page — which is most facts
// on a marketplace.
type party struct {
	accountID int64
	kind      domain.Kind
}

// The references a payload carries: the opaque ids, as plain strings, because that is what the
// recipient sees everywhere else and what a link is built from.
func orderRef(orderID int64) string     { return id.Of[id.Order](orderID).String() }
func listingRef(listingID int64) string { return id.Of[id.Listing](listingID).String() }

// notifyParties writes to every party and reports the first failure, so the bus retries the
// set rather than silently delivering half of it.
func notifyParties(ctx context.Context, svc accountapi.Service, log *slog.Logger, n notice) error {
	for _, p := range n.to {
		if p.accountID == 0 {
			continue
		}
		_, err := svc.CreateNotification(ctx, accountapi.CreateNotificationRequest{
			AccountID: id.Of[id.Account](p.accountID),
			Kind:      string(p.kind),
			Payload:   n.payload,
		})
		if err != nil {
			log.Error("create notification failed", "account_id", p.accountID, "kind", p.kind, "err", err)
			return fmt.Errorf("notify account %d: %w", p.accountID, err)
		}
	}
	return nil
}

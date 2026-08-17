package account

import (
	"context"
	"fmt"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/order"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/id"
)

// SubscribeOrderEvents turns order facts into feed rows and the mail that goes with them.
//
// One notification per interested party, because a notification belongs to an account: an
// order fact has two of them and there is no shared inbox. The handler is not idempotent
// and does not need to be — a redelivered order event costs a duplicate feed row and a
// duplicate mail, which is a cosmetic fault, and de-duplicating it would need a uniqueness
// rule the feed does not have.
//
// This file is the only place that pairs a fact with a mail template, which is what keeps
// the parameters a template names and the payload a caller builds in one another's sight.
func SubscribeOrderEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderPlacedTopic, "account", func(ctx context.Context, e order.OrderPlaced) error {
		// The sale's terms, which both the feed row and the mail render. The buyer and the
		// seller read different words about the same fact, so they get different templates.
		payload := map[string]any{
			"order_id": orderRef(e.OrderID),
			"total":    e.Total,
			"currency": e.Currency,
		}
		return notifyParties(ctx, svc, log, notice{
			category: domain.CategoryOrder,
			title:    "Order placed",
			payload:  payload,
			to: []party{
				{accountID: e.BuyerID, mail: notify.KindOrderPlaced},
				{accountID: e.SellerID, mail: notify.KindOrderReceived},
			},
		})
	})

	eventbus.Subscribe(bus, order.OrderSettledTopic, "account", func(ctx context.Context, e order.OrderSettled) error {
		title, mail := "Order completed", notify.KindOrderCompleted
		if !e.Completed {
			title, mail = "Order cancelled", notify.KindOrderCancelled
		}
		return notifyParties(ctx, svc, log, notice{
			category: domain.CategoryOrder,
			title:    title,
			payload:  map[string]any{"order_id": orderRef(e.OrderID)},
			to: []party{
				{accountID: e.BuyerID, mail: mail},
				{accountID: e.SellerID, mail: mail},
			},
		})
	})

	eventbus.Subscribe(bus, order.RefundResolvedTopic, "account", func(ctx context.Context, e order.RefundResolved) error {
		return notifyParties(ctx, svc, log, notice{
			category: domain.CategoryOrder,
			title:    "Refund decided",
			// The note travels as the moderator wrote it: the mail states the verdict and
			// quotes the reasoning, and adds nothing of its own to either.
			payload: map[string]any{
				"order_id":   orderRef(e.OrderID),
				"buyer_wins": e.BuyerWins,
				"note":       e.Note,
			},
			to: []party{
				{accountID: e.BuyerID, mail: notify.KindRefundResolved},
				{accountID: e.SellerID, mail: notify.KindRefundResolved},
			},
		})
	})

	eventbus.Subscribe(bus, order.OrderConfirmationLapsedTopic, "account", func(ctx context.Context, e order.OrderConfirmationLapsed) error {
		return notifyParties(ctx, svc, log, notice{
			category: domain.CategoryOrder,
			title:    "Order waiting on the seller",
			payload:  map[string]any{"order_id": orderRef(e.OrderID)},
			// The buyer alone is mailed. The seller is the one who did not act, and chasing
			// them is support's job — a mail from here would be this platform apologising on
			// their behalf. They still get the feed row, which is a nudge and not a letter.
			to: []party{
				{accountID: e.BuyerID, mail: notify.KindOrderUnconfirmed},
				{accountID: e.SellerID},
			},
		})
	})
}

// notice is one fact told to the accounts it concerns. A struct rather than six positional
// arguments, four of which are strings that would transpose without a compile error.
type notice struct {
	category domain.Category
	title    string
	payload  map[string]any
	to       []party
}

// party is one recipient and the mail they get, empty where the fact is worth a feed row
// but not a letter.
type party struct {
	accountID int64
	mail      notify.Kind
}

// orderRef is the order as the recipient sees it everywhere else: the opaque id, as a plain
// string, so the feed row's JSON is unchanged and a mail template can print it directly.
func orderRef(orderID int64) string { return id.Of[id.Order](orderID).String() }

// notifyParties writes to every party and reports the first failure, so the bus retries the
// set rather than silently delivering half of it.
func notifyParties(ctx context.Context, svc accountapi.Service, log *slog.Logger, n notice) error {
	for _, p := range n.to {
		if p.accountID == 0 {
			continue
		}
		_, err := svc.CreateNotification(ctx, accountapi.CreateNotificationRequest{
			AccountID: id.Of[id.Account](p.accountID),
			Category:  string(n.category),
			Title:     n.title,
			Payload:   n.payload,
			MailKind:  string(p.mail),
		})
		if err != nil {
			log.Error("create order notification failed", "account_id", p.accountID, "title", n.title, "err", err)
			return fmt.Errorf("notify account %d: %w", p.accountID, err)
		}
	}
	return nil
}

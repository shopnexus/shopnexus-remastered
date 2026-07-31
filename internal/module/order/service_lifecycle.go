package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/id"
)

// The lifecycle: the transitions nobody clicks. Each is idempotent and safe to call again,
// because a durable workflow journals a step and retries it — a second call has to be a
// no-op rather than a second effect. That is what lets the timers live in the workflow
// instead of in a table here, and what makes a sweep over the same rows harmless.

// SettlePaidSession turns a completed payment session into an order. It is the whole of
// "the money creates the order": the shipment is opened, the escrow is held, and the lines
// are linked, with no seller step anywhere.
//
// Idempotent on the origin's unique constraint: a webhook delivered twice finds the order
// already there and stops.
func (s *Service) SettlePaidSession(ctx context.Context, sessionID id.ID[id.PaymentSession]) error {
	items, err := s.repo.ItemsByPaymentSession(ctx, sessionID.Int64())
	if err != nil {
		return fmt.Errorf("read paid items: %w", err)
	}
	live := make([]*domain.Item, 0, len(items))
	for i := range items {
		if items[i].Live() && items[i].OrderID == nil {
			live = append(live, &items[i])
		}
	}
	if len(live) == 0 {
		// Nothing to do: either the order exists already or every line was cancelled.
		return nil
	}
	first := live[0]
	origin := domain.Origin{DraftID: first.DraftID, OfferID: first.OfferID}
	if existing, err := s.repo.FindOrderByOrigin(ctx, origin); err == nil {
		// The order is already there — a redelivered webhook. Linking is what may still be
		// missing, so it is retried and nothing else is.
		return s.linkItems(ctx, existing, live)
	} else if !errors.Is(err, domain.ErrOrderNotFound) {
		return fmt.Errorf("find order by origin: %w", err)
	}

	pickup, err := s.pickupSnapshot(ctx, first.SellerID)
	if err != nil {
		return err
	}
	transportID, err := s.repo.InsertTransport(ctx, first.TransportOption)
	if err != nil {
		return fmt.Errorf("open transport: %w", err)
	}
	o, err := domain.NewOrder(origin, first.BuyerID, first.SellerID, transportID,
		first.Address, pickup)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(live))
	total := int64(0)
	for _, i := range live {
		ids = append(ids, i.ID)
		total += i.TotalAmount
	}
	if err := s.repo.CreateOrder(ctx, &o, ids); err != nil {
		if errors.Is(err, domain.ErrOrderSettled) {
			// Lost the race with another delivery of the same webhook; the winner linked
			// the lines.
			return nil
		}
		return fmt.Errorf("create order: %w", err)
	}
	// The money moves after the order exists, so a failure here leaves an order whose
	// escrow the retry will hold — rather than money held against nothing.
	if err := s.finance.HoldEscrow(ctx, financeapi.EscrowRequest{
		BuyerID:        id.Of[id.Account](o.BuyerID),
		SellerID:       id.Of[id.Account](o.SellerID),
		OrderID:        id.Of[id.Order](o.ID),
		Currency:       first.Currency,
		Amount:         total,
		IdempotencyKey: fmt.Sprintf("order:%d:hold", o.ID),
	}); err != nil && !errors.Is(err, financeMovementPosted) {
		return fmt.Errorf("hold escrow: %w", err)
	}
	// The reservation becomes a sale: the units are gone for good now, not merely held.
	for _, i := range live {
		if err := s.catalog.CommitStock(ctx, catalogapi.StockMovementRequest{
			VariantID: id.Of[id.Variant](i.VariantID), Units: i.Quantity,
		}); err != nil {
			s.log.Error("commit stock after order", "item_id", i.ID, "err", err)
		}
	}
	s.publishPlaced(ctx, o, total, first.Currency)
	// The checkout's wait is over, and the order's has begun: delivery, then the escrow
	// window. Both signals are best-effort — the row already says the sale happened.
	s.timer("checkout paid", s.workflows.CheckoutPaid(ctx, sessionID.Int64()))
	s.timer("start order", s.workflows.StartOrder(ctx, o.ID))
	return nil
}

// linkItems attaches lines to an order that already exists — the half of settling a
// redelivered webhook that may still be outstanding.
func (s *Service) linkItems(ctx context.Context, o domain.Order, items []*domain.Item) error {
	ids := make([]int64, 0, len(items))
	for _, i := range items {
		ids = append(ids, i.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.repo.CreateOrder(ctx, &o, ids); err != nil && !errors.Is(err, domain.ErrOrderSettled) {
		return fmt.Errorf("link items: %w", err)
	}
	return nil
}

// ExpireDrafts closes the sessions nobody finished. A per-draft durable timer does the same
// job; this is the sweep that catches anything a timer lost.
func (s *Service) ExpireDrafts(ctx context.Context, limit int) (int, error) {
	drafts, err := s.repo.ExpiredDrafts(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read expired drafts: %w", err)
	}
	closed := 0
	for _, d := range drafts {
		if err := d.Cancel(); err != nil {
			continue
		}
		if err := s.repo.SaveDraft(ctx, d); err != nil {
			// Somebody else closed it first, which is the same outcome.
			s.log.Debug("draft already closed", "draft_id", d.ID, "err", err)
			continue
		}
		closed++
	}
	return closed, nil
}

// ExpireOffers closes negotiations nobody answered. The units were never reserved — a
// negotiation holds no stock — so there is nothing to give back.
func (s *Service) ExpireOffers(ctx context.Context, limit int) (int, error) {
	offers, err := s.repo.ExpiredOffers(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read expired offers: %w", err)
	}
	closed := 0
	for _, o := range offers {
		if err := o.Expire(); err != nil {
			continue
		}
		if err := s.repo.SaveOffer(ctx, o); err != nil {
			s.log.Debug("offer already settled", "offer_id", o.ID, "err", err)
			continue
		}
		s.postOfferCard(ctx, o, "offer expired")
		closed++
	}
	return closed, nil
}

// ReleaseDuePayouts pays the seller for orders whose escrow window has passed with no live
// refund. The order is completed in the same pass, and the completion guard is what stops a
// second payout: a completed order is no longer due.
func (s *Service) ReleaseDuePayouts(ctx context.Context, limit int) (int, error) {
	orders, err := s.repo.PayoutDue(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read due payouts: %w", err)
	}
	paid := 0
	for _, o := range orders {
		total, currency, _, err := s.orderTotal(ctx, o.ID)
		if err != nil {
			return paid, err
		}
		if total == 0 {
			continue
		}
		err = s.finance.ReleaseEscrow(ctx, financeapi.EscrowRequest{
			BuyerID:        id.Of[id.Account](o.BuyerID),
			SellerID:       id.Of[id.Account](o.SellerID),
			OrderID:        id.Of[id.Order](o.ID),
			Currency:       currency,
			Amount:         total,
			IdempotencyKey: fmt.Sprintf("order:%d:release", o.ID),
		})
		if err != nil && !errors.Is(err, financeMovementPosted) {
			s.log.Error("release escrow", "order_id", o.ID, "err", err)
			continue
		}
		if err := o.Complete(0); err != nil {
			continue
		}
		if err := s.repo.SaveOrder(ctx, o); err != nil {
			s.log.Debug("order already settled", "order_id", o.ID, "err", err)
			continue
		}
		s.publishSettled(ctx, o, true)
		paid++
	}
	return paid, nil
}

// AdvanceOverdueRefunds moves every refund whose deadline has passed — all three windows in
// one pass, which is what naming each non-terminal status for the party it waits on buys.
//
// The two states that wait on a carrier or a moderator carry no deadline, so they are never
// in this list: a human or a parcel decides those, not a clock.
func (s *Service) AdvanceOverdueRefunds(ctx context.Context, limit int) (int, error) {
	refunds, err := s.repo.OverdueRefunds(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read overdue refunds: %w", err)
	}
	advanced := 0
	for _, r := range refunds {
		settled := false
		switch r.Status {
		case domain.RefundAwaitingSeller:
			// The seller said nothing. It lands on the buyer exactly as a rejection does,
			// and the absent reason is what tells the two apart.
			err = r.LapseSellerReview()
		case domain.RefundAwaitingBuyer:
			// The buyer let the rejection stand.
			err = r.LapseBuyerAction()
		case domain.RefundReturned:
			// The seller had the goods back and did not appeal, so the buyer is paid.
			err = r.Settle(0)
			settled = err == nil
		default:
			continue
		}
		if err != nil {
			continue
		}
		if err := s.saveRefund(ctx, r); err != nil {
			s.log.Debug("refund already moved", "refund_id", r.ID, "err", err)
			continue
		}
		if settled {
			if err := s.payRefund(ctx, r); err != nil {
				s.log.Error("pay settled refund", "refund_id", r.ID, "err", err)
			}
		}
		advanced++
	}
	return advanced, nil
}

// publishSettled announces an outcome. Best-effort for the same reason publishPlaced is: the
// row is already the truth, and a bus that is down must not undo a payout.
func (s *Service) publishSettled(ctx context.Context, o domain.Order, completed bool) {
	event := OrderSettled{
		OrderID: o.ID, BuyerID: o.BuyerID, SellerID: o.SellerID, Completed: completed,
	}
	if err := publishOrderSettled(ctx, s.bus, event); err != nil {
		s.log.Error("publish order settled failed", "order_id", o.ID, "err", err)
	}
}

// publishPlaced announces the sale. Best-effort: the order is written and the money is held,
// so a bus that is down must not turn a completed purchase into a retried one.
func (s *Service) publishPlaced(ctx context.Context, o domain.Order, total int64, currency string) {
	event := OrderPlaced{
		OrderID:  o.ID,
		BuyerID:  o.BuyerID,
		SellerID: o.SellerID,
		Total:    total,
		Currency: currency,
	}
	if err := publishOrderPlaced(ctx, s.bus, event); err != nil {
		s.log.Error("publish order placed failed", "order_id", o.ID, "err", err)
	}
}

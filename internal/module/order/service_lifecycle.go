package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/provider/transport"
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
// Resumable, not merely idempotent. The order is the first thing written, so every step after
// it — linking, the escrow hold, the sale of the reserved units — has to be re-runnable
// against an order that is already there: a hold that failed once used to leave a paid buyer
// with no escrow behind their order and nothing that would ever try again.
func (s *Service) SettlePaidSession(ctx context.Context, sessionID id.ID[id.PaymentSession]) error {
	items, err := s.repo.ItemsByPaymentSession(ctx, sessionID.Int64())
	if err != nil {
		return fmt.Errorf("read paid items: %w", err)
	}
	// Every line the buyer paid for, whether or not a previous attempt already linked it. The
	// cancelled ones are the only ones left out: they were dropped before the money landed.
	paid := make([]*domain.Item, 0, len(items))
	for i := range items {
		if items[i].Live() {
			paid = append(paid, &items[i])
		}
	}
	if len(paid) == 0 {
		// Every line was cancelled, so there is no sale to write.
		return nil
	}
	first := paid[0]
	origin := domain.Origin{DraftID: first.DraftID, OfferID: first.OfferID}
	// What the buyer paid for delivery, read back from the session they paid against. It is not
	// the seller's, so it never enters the escrow — and it is not on the item, because one
	// shipment covers the order however many lines it has.
	fee, err := s.paidShippingFee(ctx, first.BuyerID, sessionID)
	if err != nil {
		return err
	}

	o, err := s.repo.FindOrderByOrigin(ctx, origin)
	switch {
	case err == nil:
		// A redelivered webhook, or a retry of an attempt that got the order written and then
		// failed. Either way the steps after the order are the ones still outstanding.
	case errors.Is(err, domain.ErrOrderNotFound):
		o, err = s.createOrder(ctx, origin, first, paid, fee)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("find order by origin: %w", err)
	}
	return s.finishSettlement(ctx, o, paid, sessionID, fee)
}

// paidShippingFee reads the delivery charge out of the session's own context. The session is what
// the buyer agreed to pay, so it is the only place the figure can be trusted from: an item total
// is the goods, and a fresh quote would be a different number by the time the money lands.
func (s *Service) paidShippingFee(ctx context.Context, buyerID int64, sessionID id.ID[id.PaymentSession]) (int64, error) {
	// Read as the buyer: finance scopes a session read to its parties, and the buyer is the one
	// who paid it. There is no system actor to borrow and inventing one would be a way past that
	// scoping for every other read too.
	session, err := s.finance.GetSession(ctx, financeapi.GetSessionRequest{
		ActorID: id.Of[id.Account](buyerID), ID: sessionID,
	})
	if err != nil {
		return 0, fmt.Errorf("read paid session: %w", err)
	}
	return decodeShippingFee(session.Data), nil
}

// createOrder opens the shipment and writes the order. Losing the origin's unique constraint
// is not a failure: somebody else wrote the same order, and their row is the one to carry on
// with.
func (s *Service) createOrder(ctx context.Context, origin domain.Origin, first *domain.Item, paid []*domain.Item, fee int64) (domain.Order, error) {
	pickup, err := s.pickupSnapshot(ctx, first.SellerID)
	if err != nil {
		return domain.Order{}, err
	}
	transportID, err := s.repo.InsertTransport(ctx, first.TransportOption, fee)
	if err != nil {
		return domain.Order{}, fmt.Errorf("open transport: %w", err)
	}
	o, err := domain.NewOrder(origin, first.BuyerID, first.SellerID, transportID,
		first.Address, pickup)
	if err != nil {
		return domain.Order{}, err
	}
	if err := s.repo.CreateOrder(ctx, &o, itemIDs(paid)); err != nil {
		if !errors.Is(err, domain.ErrOrderSettled) {
			return domain.Order{}, fmt.Errorf("create order: %w", err)
		}
		if o, err = s.repo.FindOrderByOrigin(ctx, origin); err != nil {
			return domain.Order{}, fmt.Errorf("find order by origin: %w", err)
		}
	}
	return o, nil
}

// finishSettlement is every step after the order row: the lines it covers, the escrow behind
// it, and the reservation becoming a sale. Each is keyed or guarded, so running the lot again
// against an order that already exists changes nothing — which is what makes a failed hold a
// retry rather than a buyer who paid for nothing.
func (s *Service) finishSettlement(ctx context.Context, o domain.Order, paid []*domain.Item, sessionID id.ID[id.PaymentSession], fee int64) error {
	if err := s.repo.LinkItems(ctx, o.ID, itemIDs(paid)); err != nil {
		return fmt.Errorf("link items: %w", err)
	}
	total := int64(0)
	currency := ""
	for _, i := range paid {
		total += i.TotalAmount
		currency = i.Currency
	}
	if err := s.finance.HoldEscrow(ctx, financeapi.EscrowRequest{
		BuyerID:        id.Of[id.Account](o.BuyerID),
		SellerID:       id.Of[id.Account](o.SellerID),
		OrderID:        id.Of[id.Order](o.ID),
		Currency:       currency,
		Amount:         total,
		ShippingFee:    fee,
		IdempotencyKey: fmt.Sprintf("order:%d:hold", o.ID),
	}); err != nil && !errors.Is(err, financeMovementPosted) {
		return fmt.Errorf("hold escrow: %w", err)
	}
	// The reservation becomes a sale: the units are gone for good now, not merely held. Keyed
	// per line, so a retried settlement does not sell them twice — and an error here is
	// returned rather than logged, because the retry is the only thing that would fix it.
	for _, i := range paid {
		if err := s.commitItemStock(ctx, o.ID, *i); err != nil {
			return err
		}
	}
	s.publishPlaced(ctx, o, total, currency)
	// The checkout's wait is over, and the order's has begun: delivery, then the escrow
	// window. Both signals are best-effort — the row already says the sale happened.
	s.timer("checkout paid", s.workflows.CheckoutPaid(ctx, sessionID.Int64()))
	s.timer("start order", s.workflows.StartOrder(ctx, o.ID))
	// The parcel is handed to the courier the buyer paid for. Best-effort: the sale has happened
	// and the money has moved, so a carrier that is down is a booking to retry rather than an
	// order to refuse — `RetryUnbookedShipments` is the net under it.
	s.bookShipment(ctx, o, deref(paid))
	return nil
}

// bookShipment tells the carrier there is a parcel. It is what the delivery fee was collected
// for, so a shipment nobody booked is money the platform took and did nothing with.
//
// Never fatal to the caller: the order exists either way, and the seller can still report the
// handover. What the courier gives back — its own reference, a label — is written onto the
// shipment, which is also the marker that says this parcel no longer needs booking.
func (s *Service) bookShipment(ctx context.Context, o domain.Order, lines []domain.Item) {
	t, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		s.log.Error("read shipment to book", "order_id", o.ID, "err", err)
		return
	}
	if t.Booked() {
		return
	}
	items := make([]transport.ItemMetadata, 0, len(lines))
	for _, i := range lines {
		items = append(items, transport.ItemMetadata{VariantID: i.VariantID, Quantity: i.Quantity})
	}
	booked, err := s.transport.Create(ctx, transport.CreateParams{
		Items:       items,
		FromAddress: addressLine(o.PickupAddress),
		ToAddress:   addressLine(o.Address),
		Option:      t.Option,
	})
	if err != nil {
		s.log.Error("book shipment with carrier", "order_id", o.ID, "option", t.Option, "err", err)
		return
	}
	data, err := bookingData(booked)
	if err != nil {
		s.log.Error("encode carrier booking", "order_id", o.ID, "err", err)
		return
	}
	if err := s.repo.BookTransport(ctx, t.ID, data); err != nil {
		// Somebody else booked it first, or the parcel has already moved on. Either way this
		// pass has nothing to add.
		s.log.Debug("shipment already booked", "order_id", o.ID, "err", err)
	}
}

// bookingData is what the carrier said, kept whole: its reference is what a later track or cancel
// is made with, and the rest is provider-shaped and not published.
func bookingData(t transport.Transport) ([]byte, error) {
	out := map[string]any{"provider_ref": t.ID}
	if len(t.Data) > 0 {
		out["provider_data"] = json.RawMessage(t.Data)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode booking: %w", err)
	}
	return data, nil
}

// RecordCarrierCheckpoint is the courier's own report, arriving on the provider's webhook rather
// than from the seller. It carries the carrier's reference, so the shipment is found by that.
//
// The provider's vocabulary is translated here and nowhere else: a courier's "processing" is this
// module's `in-transit`, and a status it does not use is ignored rather than guessed at. The same
// forward-only rule the seller's route obeys applies, so a checkpoint that arrives late — they do
// arrive out of order — cannot un-deliver a parcel.
func (s *Service) RecordCarrierCheckpoint(ctx context.Context, ref, status string) error {
	t, err := s.repo.FindTransportByRef(ctx, ref)
	if err != nil {
		return err
	}
	next, ok := carrierCheckpoints[status]
	if !ok {
		s.log.Debug("carrier status ignored", "ref", ref, "status", status)
		return nil
	}
	if _, err := s.advanceLeg(ctx, t.ID, next); err != nil {
		// A report that would move the parcel backwards is not an error the carrier can fix, so
		// it is accepted and dropped rather than retried for ever.
		if errors.Is(err, domain.ErrTransportSettled) {
			return nil
		}
		return err
	}
	return nil
}

// carrierCheckpoints maps a provider's status vocabulary onto this module's. Two are deliberately
// absent: `pending`, because a parcel the courier has accepted is already past it here, and
// `cancelled`, because calling off a delivery is this platform's decision to make and arrives
// through the order rather than from the carrier.
var carrierCheckpoints = map[string]string{
	string(transport.StatusProcessing): domain.TransportInTransit,
	string(transport.StatusSuccess):    domain.TransportDelivered,
	string(transport.StatusFailed):     domain.TransportFailed,
}

// RetryUnbookedShipments books the parcels a carrier refused or never heard about. The same
// method the settlement calls, so there is one definition of "booked" — and it is idempotent, so
// a parcel the courier did accept is skipped rather than booked twice.
func (s *Service) RetryUnbookedShipments(ctx context.Context, limit int) (int, error) {
	orders, err := s.repo.UnbookedTransports(ctx, time.Now().Add(-bookingGrace), limit)
	if err != nil {
		return 0, fmt.Errorf("read unbooked shipments: %w", err)
	}
	booked := 0
	for _, orderID := range orders {
		o, err := s.repo.FindOrder(ctx, orderID)
		if err != nil {
			s.log.Error("read order to book", "order_id", orderID, "err", err)
			continue
		}
		lines, err := s.repo.OrderItems(ctx, orderID)
		if err != nil {
			s.log.Error("read order lines to book", "order_id", orderID, "err", err)
			continue
		}
		s.bookShipment(ctx, o, lines)
		if t, err := s.repo.FindTransport(ctx, o.TransportID); err == nil && t.Booked() {
			booked++
		}
	}
	return booked, nil
}

// deref is the settlement's lines as values: the booking only reads them.
func deref(items []*domain.Item) []domain.Item {
	out := make([]domain.Item, 0, len(items))
	for _, i := range items {
		out = append(out, *i)
	}
	return out
}

func itemIDs(items []*domain.Item) []int64 {
	out := make([]int64, 0, len(items))
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
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

// ExpireCheckouts releases the stock of checkouts nobody paid for. A per-session durable timer
// does this promptly; this is the sweep, and without it the `off` deployment loses those units
// for ever — nothing else ever looks at a reserved-but-unpaid line.
//
// A paid line is refused by CancelItem itself, which asks finance rather than trusting the age
// of the row.
func (s *Service) ExpireCheckouts(ctx context.Context, limit int) (int, error) {
	items, err := s.repo.UnpaidItems(ctx, time.Now().Add(-checkoutWindow), limit)
	if err != nil {
		return 0, fmt.Errorf("read unpaid items: %w", err)
	}
	cancelled := 0
	for _, i := range items {
		if err := s.cancelLine(ctx, &i, i.BuyerID); err != nil {
			s.log.Debug("unpaid line not cancellable", "item_id", i.ID, "err", err)
			continue
		}
		cancelled++
	}
	return cancelled, nil
}

// expirableOffer is what an expiry may overwrite: a standing proposal nobody answered and an
// agreed price nobody checked out. Never `checked-out` — that clock is the payment session's.
var expirableOffer = []string{domain.OfferActive, domain.OfferAccepted}

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
		// Either wait may be the one that lapsed, so both statuses are writable here — and
		// `checked-out` is not, which is what keeps a paying buyer's offer out of the sweep.
		if err := s.repo.SaveOffer(ctx, o, expirableOffer); err != nil {
			s.log.Debug("offer already settled", "offer_id", o.ID, "err", err)
			continue
		}
		s.postOfferCard(ctx, o, cardOfferExpired)
		closed++
	}
	return closed, nil
}

// ExpireOffer closes one negotiation whose deadline has passed — the per-offer version, which is
// what a run of its own calls. Asking the bulk pass for a limit of one would close whichever
// negotiation was oldest rather than the one this run is following.
//
// Idempotent: Expire refuses a row that is already cancelled or checked out, and the save is
// guarded by the status it moves from.
func (s *Service) ExpireOffer(ctx context.Context, offerID int64) error {
	o, err := s.repo.FindOffer(ctx, offerID)
	if err != nil {
		return fmt.Errorf("find offer: %w", err)
	}
	if time.Now().Before(o.ExpiresAt) {
		// The clock moved — an acceptance restarts it — so there is nothing due here yet.
		return nil
	}
	if err := o.Expire(); err != nil {
		return nil
	}
	if err := s.repo.SaveOffer(ctx, o, expirableOffer); err != nil {
		s.log.Debug("offer already settled", "offer_id", o.ID, "err", err)
		return nil
	}
	s.postOfferCard(ctx, o, cardOfferExpired)
	return nil
}

// ReleaseDuePayouts pays the seller for every order whose escrow window has passed. The bulk
// pass; ReleasePayout is the same work for one order, which is what a per-order run calls —
// a run that asked for this with a limit of one acted on whichever order was oldest instead
// of its own.
func (s *Service) ReleaseDuePayouts(ctx context.Context, limit int) (int, error) {
	orders, err := s.repo.PayoutDue(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read due payouts: %w", err)
	}
	paid := 0
	for _, o := range orders {
		released, err := s.releasePayout(ctx, o)
		if err != nil {
			return paid, err
		}
		if released {
			paid++
		}
	}
	return paid, nil
}

// ReleasePayout is one order's escrow release, driven by the run that follows that order.
// Idempotent: an order that is no longer due is left alone.
func (s *Service) ReleasePayout(ctx context.Context, orderID id.ID[id.Order]) error {
	o, err := s.repo.FindOrder(ctx, orderID.Int64())
	if err != nil {
		return fmt.Errorf("find order: %w", err)
	}
	due := o.PayoutDue()
	if o.Settled() || due == nil || due.After(time.Now()) {
		return nil
	}
	if _, err := s.releasePayout(ctx, o); err != nil {
		return err
	}
	return nil
}

// releasePayout completes the order and then releases the money. Completing first is the whole
// guard: `ClaimPayout` takes the order's advisory lock, re-reads whether anything is disputing
// the money and writes the outcome in one transaction, so a refund committed a moment ago is
// seen rather than stepped over. Whoever writes the order's outcome wins the escrow.
func (s *Service) releasePayout(ctx context.Context, o domain.Order) (bool, error) {
	total, currency, _, err := s.orderTotal(ctx, o.ID)
	if err != nil {
		return false, err
	}
	if total == 0 {
		return false, nil
	}
	if err := s.repo.ClaimPayout(ctx, &o); err != nil {
		// A refund got there first, or another pass did. Either way this one is not due.
		s.log.Debug("payout not claimable", "order_id", o.ID, "err", err)
		return false, nil
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
		// The order is claimed, so nobody else will pay this out; the money is still in escrow
		// and the order stays on the stranded list until a retry gets it out.
		s.log.Error("release escrow", "order_id", o.ID, "err", err)
		return false, nil
	}
	s.recordRelease(ctx, o)
	s.publishSettled(ctx, o, true)
	return true, nil
}

// recordRelease takes the order off the stranded list. Best-effort: the money has already moved
// and the release key makes a repeat a no-op, so a failure here costs one extra retry rather
// than a second payout.
func (s *Service) recordRelease(ctx context.Context, o domain.Order) {
	o.MarkPayoutReleased()
	if err := s.repo.MarkPayoutReleased(ctx, o); err != nil {
		s.log.Error("record payout release", "order_id", o.ID, "err", err)
	}
}

// RetryClaimedPayouts is the second half of a payout that got as far as claiming the order and
// then could not reach finance. The claim is what stopped a second pass paying it out, so
// without this the escrow would sit held for ever; the release key makes calling it again for
// an order that did settle a no-op.
func (s *Service) RetryClaimedPayouts(ctx context.Context, limit int) (int, error) {
	orders, err := s.repo.ClaimedPayouts(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("read claimed payouts: %w", err)
	}
	retried, stranded := 0, 0
	for _, o := range orders {
		total, currency, _, err := s.orderTotal(ctx, o.ID)
		if err != nil {
			return retried, err
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
			// One line per order per pass at debug, and one summary below: an order that will
			// never release would otherwise log an error on every tick for ever, which buries
			// the first occurrence of anything else.
			s.log.Debug("retry release escrow", "order_id", o.ID, "err", err)
			stranded++
			continue
		}
		s.recordRelease(ctx, o)
		retried++
	}
	if stranded > 0 {
		// Money the seller is owed and did not get. One line, every pass, until it is fixed —
		// this is the signal to alert on, and it must not go quiet while the debt stands.
		s.log.Error("escrow releases stranded", "orders", stranded,
			"oldest_completed_at", orders[0].CompletedAt)
	}
	return retried, nil
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
		moved, err := s.advanceRefund(ctx, r)
		if err != nil {
			return advanced, err
		}
		if moved {
			advanced++
		}
	}
	return advanced, nil
}

// AdvanceRefund is one case's overdue window, driven by the run that waits on it. The run is
// keyed by the refund and the status it was started for, but the decision is re-read here:
// the row is the truth about whose clock is running.
func (s *Service) AdvanceRefund(ctx context.Context, refundID id.ID[id.Refund]) error {
	r, err := s.repo.FindRefund(ctx, refundID.Int64())
	if err != nil {
		return fmt.Errorf("find refund: %w", err)
	}
	if _, err := s.advanceRefund(ctx, r); err != nil {
		return err
	}
	return nil
}

// advanceRefund applies whichever window ran out. The settle path moves the money *before* the
// row goes terminal: `accepted` is the end of the case, and writing it first left the payout
// sweep free to hand the seller money the buyer had been awarded.
func (s *Service) advanceRefund(ctx context.Context, r domain.Refund) (bool, error) {
	if r.Settled() || r.DeadlineAt == nil || r.DeadlineAt.After(time.Now()) {
		return false, nil
	}
	o, err := s.repo.FindOrder(ctx, r.OrderID)
	if err != nil {
		return false, fmt.Errorf("find order: %w", err)
	}
	if o.Settled() {
		// The order was cancelled or paid out under the case; there is no escrow left to move.
		return false, nil
	}
	switch r.Status {
	case domain.RefundAwaitingSeller:
		// The seller said nothing. It lands on the buyer exactly as a rejection does, and the
		// absent reason is what tells the two apart.
		if err := r.LapseSellerReview(); err != nil {
			return false, nil
		}
	case domain.RefundAwaitingBuyer:
		// The buyer let the rejection stand.
		if err := r.LapseBuyerAction(); err != nil {
			return false, nil
		}
	case domain.RefundReturned:
		// The seller had the goods back and did not appeal, so the buyer is paid.
		if err := r.Settle(); err != nil {
			return false, nil
		}
		if err := s.settleRefund(ctx, r, o, nil); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
	if err := s.saveRefund(ctx, r); err != nil {
		s.log.Debug("refund already moved", "refund_id", r.ID, "err", err)
		return false, nil
	}
	return true, nil
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

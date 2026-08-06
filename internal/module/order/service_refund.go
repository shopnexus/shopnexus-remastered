package order

import (
	"context"
	"fmt"
	"time"

	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// CreateRefund opens a case on a delivered order. One live refund per order — a refund
// covers the whole order, so a second could not be about anything — and it starts on the
// seller's clock: they get to accept or refuse before anybody else is involved.
func (s *Service) CreateRefund(ctx context.Context, req orderapi.CreateRefundRequest) (orderapi.Refund, error) {
	o, err := s.involved(ctx, req.ActorID, req.OrderID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if o.BuyerID != req.ActorID.Int64() {
		return orderapi.Refund{}, domain.ErrNotTheBuyer
	}
	// A refund is about goods that arrived or should have: before the parcel moves the buyer
	// cancels instead, and after the escrow is paid out there is nothing left to return.
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrRefundNotDue
	}
	attachments := resourceKeys(req.Attachments)
	if err := s.requireResources(ctx, attachments); err != nil {
		return orderapi.Refund{}, err
	}
	r, err := domain.NewRefund(o.ID, o.BuyerID, req.Reason, attachments)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.repo.InsertRefund(ctx, &r); err != nil {
		return orderapi.Refund{}, fmt.Errorf("insert refund: %w", err)
	}
	// The case has its own clock, and the order's escrow window must not release money the
	// buyer is disputing.
	s.refundTimers(ctx, r)
	s.timer("refund raised", s.workflows.RefundRaised(ctx, o.ID))
	return s.refundView(ctx, r)
}

func (s *Service) ListRefunds(ctx context.Context, req orderapi.ListRefundsRequest) (orderapi.RefundPage, error) {
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.RefundPage{}, err
	}
	filter := port.RefundFilter{Cursor: cursor}
	if req.Status != "" {
		filter.Statuses = []string{req.Status}
	}
	if req.Role == orderapi.RoleSeller {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListRefunds(ctx, filter)
	if err != nil {
		return orderapi.RefundPage{}, fmt.Errorf("list refunds: %w", err)
	}
	rows, meta := page(rows, req.Limit, func(r domain.Refund) (time.Time, int64) {
		return r.CreatedAt, r.ID
	})
	out := make([]orderapi.Refund, 0, len(rows))
	for _, r := range rows {
		view, err := s.refundView(ctx, r)
		if err != nil {
			return orderapi.RefundPage{}, err
		}
		out = append(out, view)
	}
	return orderapi.RefundPage{Data: out, Meta: meta}, nil
}

func (s *Service) GetRefund(ctx context.Context, req orderapi.RefundRequest) (orderapi.Refund, error) {
	r, _, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// WithdrawRefund is the buyer dropping the case. Only while the seller has not decided:
// after that there is a verdict on the record, and withdrawing would erase it.
func (s *Service) WithdrawRefund(ctx context.Context, req orderapi.RefundRequest) error {
	r, _, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return err
	}
	if r.BuyerID != req.ActorID.Int64() {
		return domain.ErrNotTheBuyer
	}
	// Its own terminal status, not a rejection: stored as one, a withdrawal would be
	// indistinguishable from a seller who won the case.
	from := r.Status
	if err := r.Withdraw(); err != nil {
		return err
	}
	if err := s.saveRefund(ctx, r, from); err != nil {
		return err
	}
	return nil
}

// AddRefundAttachments tops up the evidence while the case is open. A closed one is the
// record a verdict was reached on, so nothing is added to it afterwards.
func (s *Service) AddRefundAttachments(ctx context.Context, req orderapi.AddRefundAttachmentsRequest) (orderapi.Refund, error) {
	r, _, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	attachments := resourceKeys(req.Attachments)
	if err := s.requireResources(ctx, attachments); err != nil {
		return orderapi.Refund{}, err
	}
	// Nothing moves, so the guard is the status the caller read: evidence added to a case that
	// closed a moment ago belongs to the record a verdict was reached on.
	from := r.Status
	if err := r.AddAttachments(attachments); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r, from); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// AcceptRefund is the seller granting it. The goods come back first: the return leg exists
// only from here, because a refund that never gets granted never ships anything.
func (s *Service) AcceptRefund(ctx context.Context, req orderapi.RefundRequest) (orderapi.Refund, error) {
	r, o, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if o.SellerID != req.ActorID.Int64() {
		return orderapi.Refund{}, domain.ErrNotTheSeller
	}
	// The escrow is what a refund moves, so an order that has already paid out or been
	// cancelled has nothing left for this case to decide.
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrOrderSettled
	}
	from := r.Status
	if err := r.Accept(); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.openReturnLeg(ctx, &r, o); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r, from); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// openReturnLeg books the parcel back, buyer to seller. There is no leg the other way: a seller
// who wins was never sent anything.
//
// Both ways into `returning` come through here — the seller accepting, and a staff verdict for
// the buyer before the goods moved — because the leg is the only exit from that state, and one
// without it strands the escrow with nobody on a clock.
func (s *Service) openReturnLeg(ctx context.Context, r *domain.Refund, o domain.Order) error {
	if r.ReturnTransportID != nil {
		return nil
	}
	transport, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		return fmt.Errorf("find transport: %w", err)
	}
	// No fee on the return leg: the buyer paid to have the goods delivered, not to send them
	// back, and who bears the return carriage is the verdict's business rather than a charge
	// raised here.
	returnID, err := s.repo.InsertTransport(ctx, transport.Option, 0)
	if err != nil {
		return fmt.Errorf("open return transport: %w", err)
	}
	if err := r.StartReturn(returnID); err != nil {
		return err
	}
	return nil
}

// AdvanceReturnShipment records a checkpoint on the leg carrying the goods back, and marking it
// delivered is what opens the seller's inspection window. Either party may report it: the buyer
// posted the parcel and the seller received it, and requiring the seller alone would let one who
// simply never confirms strand the escrow — which is exactly what having no writer at all did.
// A buyer who claims a delivery that did not happen is answered by the seller escalating, which
// is what that window is for.
func (s *Service) AdvanceReturnShipment(ctx context.Context, req orderapi.AdvanceReturnShipmentRequest) (orderapi.Refund, error) {
	r, o, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if r.ReturnTransportID == nil {
		return orderapi.Refund{}, domain.ErrNoReturnLeg
	}
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrOrderSettled
	}
	leg, err := s.advanceLeg(ctx, *r.ReturnTransportID, req.Status)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if !leg.Delivered() {
		return s.refundView(ctx, r)
	}
	from := r.Status
	if err := r.MarkReturned(); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r, from); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// EscalateRefund records that staff have been asked to decide. Both entries are the seller's:
// instead of refusing a refund on their own word they raise a ticket, and after the goods come
// back they may say what arrived is not what the buyer's evidence showed. Trust calls this when
// it opens the ticket, so the refund's status and the ticket cannot disagree — there is no
// route, because an escalation with no ticket behind it is a case nobody would ever work.
//
// Idempotent: a refund already with staff is answered rather than refused, since trust retries.
func (s *Service) EscalateRefund(ctx context.Context, req orderapi.EscalateRefundRequest) (orderapi.Refund, error) {
	o, err := s.involved(ctx, req.ActorID, req.OrderID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	r, err := s.repo.LiveRefundOnOrder(ctx, o.ID)
	if err != nil {
		return orderapi.Refund{}, fmt.Errorf("find live refund: %w", err)
	}
	// Already with staff: answered rather than refused, because trust retries and because the
	// timeout path moves the case here itself — the ticket then follows for a refund whose status
	// has already changed, with no actor to check.
	if r.Status == domain.RefundDisputed {
		return s.refundView(ctx, r)
	}
	// A verdict moves the order's escrow, so a case over an order that has already paid out or
	// been cancelled has nothing staff could decide — and the ticket trust is about to open
	// closes only on a published verdict, so escalating one would strand it for ever.
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrOrderSettled
	}
	// Only the seller escalates, from either of their two windows. The buyer opened the case;
	// making them open it again is what used to lose them the money.
	switch r.Status {
	case domain.RefundAwaitingSeller, domain.RefundReturned:
		if o.SellerID != req.ActorID.Int64() {
			return orderapi.Refund{}, domain.ErrNotTheSeller
		}
	default:
		return orderapi.Refund{}, domain.ErrRefundNotEscalatable
	}
	from := r.Status
	if err := r.Escalate(); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r, from); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// AdminResolveRefund is the verdict. The money moves before the row goes terminal, and the refund
// and the order it closes are written together: an accepted refund over an order the payout sweep
// can still see is money paid to both parties.
func (s *Service) AdminResolveRefund(ctx context.Context, req orderapi.ResolveRefundRequest) (orderapi.Refund, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return orderapi.Refund{}, err
	}
	r, err := s.repo.FindRefund(ctx, req.ID.Int64())
	if err != nil {
		return orderapi.Refund{}, fmt.Errorf("find refund: %w", err)
	}
	o, err := s.repo.FindOrder(ctx, r.OrderID)
	if err != nil {
		return orderapi.Refund{}, fmt.Errorf("find order: %w", err)
	}
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrOrderSettled
	}
	// There are no rounds: `ReturnedAt IS NULL` is what tells the two situations apart. A buyer
	// who wins before the goods came back is granted the refund and they travel first; one who
	// wins after has nothing left to ship, so the escrow goes back and the order closes.
	if err := r.Resolve(req.BuyerWins); err != nil {
		return orderapi.Refund{}, err
	}
	if r.Status == domain.RefundReturning {
		// A granted refund is one whose goods come back. Without the leg the case would sit in
		// `returning` with nobody on a clock.
		if err := s.openReturnLeg(ctx, &r, o); err != nil {
			return orderapi.Refund{}, err
		}
	}
	if r.Status == domain.RefundAccepted {
		if err := s.settleRefund(ctx, r, o, domain.RefundDisputed); err != nil {
			return orderapi.Refund{}, err
		}
	} else {
		// Guarded on the case still being with staff, so two moderators pressing at once do not
		// both decide it — the loser is told the verdict is in rather than publishing a second one.
		if err := s.repo.SaveRefundOutcome(ctx, r, nil, domain.RefundDisputed); err != nil {
			return orderapi.Refund{}, fmt.Errorf("save refund verdict: %w", err)
		}
		s.refundTimers(ctx, r)
	}
	s.publishResolved(ctx, r, o, req)
	return s.refundView(ctx, r)
}

// publishResolved announces the verdict, so trust can close the ticket that asked for it.
// Best-effort like the module's other publishes: the money has moved and the rows are written, so
// a bus that is down must not turn a decided case into a retried one.
func (s *Service) publishResolved(ctx context.Context, r domain.Refund, o domain.Order,
	req orderapi.ResolveRefundRequest) {
	event := RefundResolved{
		RefundID: r.ID, OrderID: o.ID,
		ModeratorID: req.ActorID.Int64(), BuyerWins: req.BuyerWins, Note: req.Note,
	}
	if err := publishRefundResolved(ctx, s.bus, event); err != nil {
		s.log.Error("publish refund resolved failed", "refund_id", r.ID, "err", err)
	}
}

// settleRefund pays the buyer back and closes the case and the order it covers. `from` is the
// status the case is leaving, which guards both writes.
//
// The money moves first, and only then does the row go terminal. The other way round leaves an
// `accepted` refund the escrow never reached — nothing retries it, because `accepted` is the
// end of the case, and the payout sweep then hands the seller the money the buyer was awarded.
// A transfer that landed and a write that did not converges differently on the two paths: an
// uncontested return is still overdue, so the sweep calls this again with the same key, while a
// `disputed` case has no clock and waits for a moderator to press the verdict again — which is
// safe, because the transfer is keyed on the order.
func (s *Service) settleRefund(ctx context.Context, r domain.Refund, o domain.Order, from string) error {
	if err := s.refundEscrow(ctx, o, 0); err != nil {
		return err
	}
	// The order goes with it: an order still open under a settled refund is one the payout
	// sweep would read as due, which is why the two are written in one transaction.
	closed := o
	if err := closed.Cancel(false); err != nil {
		return err
	}
	if err := s.repo.SaveRefundOutcome(ctx, r, &closed, from); err != nil {
		return fmt.Errorf("save refund settlement: %w", err)
	}
	s.publishSettled(ctx, closed, false)
	s.uncommitOrderStock(ctx, closed)
	s.timer("refund resolved", s.workflows.RefundResolved(ctx, closed.ID, true))
	return nil
}

// refundParty reads a refund the caller is a party to, and the order behind it. A refund on
// somebody else's order is not found rather than forbidden.
func (s *Service) refundParty(ctx context.Context, actorID id.ID[id.Account], refundID id.ID[id.Refund]) (domain.Refund, domain.Order, error) {
	r, err := s.repo.FindRefund(ctx, refundID.Int64())
	if err != nil {
		return domain.Refund{}, domain.Order{}, fmt.Errorf("find refund: %w", err)
	}
	o, err := s.repo.FindOrder(ctx, r.OrderID)
	if err != nil {
		return domain.Refund{}, domain.Order{}, fmt.Errorf("find order: %w", err)
	}
	if !o.Involves(actorID.Int64()) {
		return domain.Refund{}, domain.Order{}, domain.ErrRefundNotFound
	}
	return r, o, nil
}

func (s *Service) refundView(ctx context.Context, r domain.Refund) (orderapi.Refund, error) {
	evidence, err := s.resources(ctx, r.Attachments)
	if err != nil {
		return orderapi.Refund{}, err
	}
	return orderapi.Refund{
		ID:              id.Of[id.Refund](r.ID),
		OrderID:         id.Of[id.Order](r.OrderID),
		BuyerID:         id.Of[id.Account](r.BuyerID),
		Status:          r.Status,
		Reason:          r.Reason,
		Attachments:     pick(evidence, r.Attachments),
		DeadlineAt:      r.DeadlineAt,
		SellerDecidedAt: r.SellerDecidedAt,
		ReturnedAt:      r.ReturnedAt,
		CreatedAt:       r.CreatedAt,
	}, nil
}

// saveRefund writes a non-terminal transition and starts the clock the new status carries.
// `from` is the status the caller read, which is the write's guard: a case that moved under the
// caller — an escalation, another party's decision — makes this lose rather than overwrite it.
func (s *Service) saveRefund(ctx context.Context, r domain.Refund, from string) error {
	if err := s.repo.SaveRefund(ctx, r, from); err != nil {
		return fmt.Errorf("save refund: %w", err)
	}
	s.refundTimers(ctx, r)
	return nil
}

// refundTimers opens the wait the refund's new state implies: one run per window, keyed by the
// refund *and* the status it is waiting on, started by the transition that entered that state.
// One run for all three windows cannot work — a durable promise is single-assignment per name,
// so the second window's wait returns instantly and the run spins — and the state the run was
// started for is what lets it check, when it wakes, that it is still the one holding things up.
//
// A case that closed also releases the order's escrow window, which was held waiting on
// exactly this verdict.
func (s *Service) refundTimers(ctx context.Context, r domain.Refund) {
	if r.DeadlineAt != nil {
		s.timer("refund window", s.workflows.StartRefundWindow(ctx, r.ID, r.Status))
	}
	if r.Settled() {
		s.timer("refund resolved",
			s.workflows.RefundResolved(ctx, r.OrderID, r.Status == domain.RefundAccepted))
	}
}

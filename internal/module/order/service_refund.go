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
	if err := r.Withdraw(); err != nil {
		return err
	}
	if err := s.saveRefund(ctx, r); err != nil {
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
	if err := r.AddAttachments(attachments); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r); err != nil {
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
	if err := r.Accept(); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.openReturnLeg(ctx, &r, o); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// openReturnLeg books the parcel back, buyer to seller. There is no leg the other way: a seller
// who wins round one was never sent anything.
//
// Both ways into `returning` come through here — the seller accepting, and a moderator ruling
// for the buyer in round one — because the leg is the only exit from that state, and one
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
// A buyer who claims a delivery that did not happen is answered by round two, which is what that
// window is for.
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
	if err := r.MarkReturned(); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// RejectRefund refuses it, with a reason. The buyer's move next: escalate to a moderator, or
// let the window lapse.
func (s *Service) RejectRefund(ctx context.Context, req orderapi.RejectRefundRequest) (orderapi.Refund, error) {
	r, o, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Refund{}, err
	}
	if o.SellerID != req.ActorID.Int64() {
		return orderapi.Refund{}, domain.ErrNotTheSeller
	}
	if o.Settled() {
		return orderapi.Refund{}, domain.ErrOrderSettled
	}
	if err := r.Reject(req.Reason); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.saveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, err
	}
	return s.refundView(ctx, r)
}

// OpenDispute escalates to a moderator. Round one is the buyer after a rejection; round two
// is the seller after the goods came back and are not what they were told to expect.
func (s *Service) OpenDispute(ctx context.Context, req orderapi.OpenDisputeRequest) (orderapi.Dispute, error) {
	r, o, err := s.refundParty(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Dispute{}, err
	}
	round := int16(1)
	switch r.Status {
	case domain.RefundAwaitingBuyer:
		if r.BuyerID != req.ActorID.Int64() {
			return orderapi.Dispute{}, domain.ErrNotTheBuyer
		}
	case domain.RefundReturned:
		if o.SellerID != req.ActorID.Int64() {
			return orderapi.Dispute{}, domain.ErrNotTheSeller
		}
		round = 2
	default:
		return orderapi.Dispute{}, domain.ErrNotAwaitingBuyer
	}
	if err := r.Escalate(); err != nil {
		return orderapi.Dispute{}, err
	}
	d, err := domain.NewDispute(r.ID, req.ActorID.Int64(), round, req.Reason)
	if err != nil {
		return orderapi.Dispute{}, err
	}
	if err := s.repo.InsertDispute(ctx, &d); err != nil {
		return orderapi.Dispute{}, fmt.Errorf("insert dispute: %w", err)
	}
	if err := s.saveRefund(ctx, r); err != nil {
		return orderapi.Dispute{}, err
	}
	return toAPIDispute(d), nil
}

// AdminListDisputes is the moderator queue, oldest first — the order it should be worked.
func (s *Service) AdminListDisputes(ctx context.Context, req orderapi.ListDisputesRequest) (orderapi.DisputePage, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return orderapi.DisputePage{}, err
	}
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.DisputePage{}, err
	}
	rows, err := s.repo.ListOpenDisputes(ctx, cursor)
	if err != nil {
		return orderapi.DisputePage{}, fmt.Errorf("list disputes: %w", err)
	}
	rows, meta := page(rows, req.Limit, func(d domain.Dispute) (time.Time, int64) {
		return d.CreatedAt, d.ID
	})
	out := make([]orderapi.Dispute, 0, len(rows))
	for _, d := range rows {
		out = append(out, toAPIDispute(d))
	}
	return orderapi.DisputePage{Data: out, Meta: meta}, nil
}

// AdminRuleDispute applies the verdict to the round and to the refund behind it. A buyer who
// wins round one gets the goods collected and then the money; one who wins round two gets
// the money, because the goods are already back.
//
// The money moves before either row is written, and the two rows are written together: a ruled
// round over a still-disputed refund is a state no path can reach, and an accepted refund over
// an order the payout sweep can still see is money paid to both parties.
func (s *Service) AdminRuleDispute(ctx context.Context, req orderapi.RuleDisputeRequest) (orderapi.Dispute, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return orderapi.Dispute{}, err
	}
	d, err := s.repo.FindDispute(ctx, req.ID.Int64())
	if err != nil {
		return orderapi.Dispute{}, fmt.Errorf("find dispute: %w", err)
	}
	if err := d.Rule(req.ActorID.Int64(), req.BuyerWins, req.Note); err != nil {
		return orderapi.Dispute{}, err
	}
	r, err := s.repo.FindRefund(ctx, d.RefundID)
	if err != nil {
		return orderapi.Dispute{}, fmt.Errorf("find refund: %w", err)
	}
	o, err := s.repo.FindOrder(ctx, r.OrderID)
	if err != nil {
		return orderapi.Dispute{}, fmt.Errorf("find order: %w", err)
	}
	if o.Settled() {
		return orderapi.Dispute{}, domain.ErrOrderSettled
	}
	if err := r.Rule(req.BuyerWins); err != nil {
		return orderapi.Dispute{}, err
	}
	if r.Status == domain.RefundReturning {
		// Round one for the buyer grants the refund, and a granted refund is one whose goods
		// come back. Without the leg the case would sit in `returning` with nobody on a clock.
		if err := s.openReturnLeg(ctx, &r, o); err != nil {
			return orderapi.Dispute{}, err
		}
	}
	if r.Status == domain.RefundAccepted {
		if err := s.settleRefund(ctx, r, o, &d); err != nil {
			return orderapi.Dispute{}, err
		}
		return toAPIDispute(d), nil
	}
	if err := s.repo.SaveRefundOutcome(ctx, r, &d, nil); err != nil {
		return orderapi.Dispute{}, fmt.Errorf("save dispute ruling: %w", err)
	}
	s.refundTimers(ctx, r)
	return toAPIDispute(d), nil
}

// settleRefund pays the buyer back and closes the case and the order it covers.
//
// The money moves first, and only then does the row go terminal. The other way round leaves an
// `accepted` refund the escrow never reached — nothing retries it, because `accepted` is the
// end of the case, and the payout sweep then hands the seller the money the buyer was awarded.
// A transfer that landed and a write that did not is the recoverable half: the refund is still
// live, its window is still overdue, and the sweep calls this again with the same key.
func (s *Service) settleRefund(ctx context.Context, r domain.Refund, o domain.Order, d *domain.Dispute) error {
	if err := s.refundEscrow(ctx, o, 0); err != nil {
		return err
	}
	// The order goes with it: an order still open under a settled refund is one the payout
	// sweep would read as due, which is why the two are written in one transaction.
	closed := o
	if err := closed.Cancel(false); err != nil {
		return err
	}
	if err := s.repo.SaveRefundOutcome(ctx, r, d, &closed); err != nil {
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
		RejectionReason: r.RejectionReason,
		SellerDecidedAt: r.SellerDecidedAt,
		ReturnedAt:      r.ReturnedAt,
		CreatedAt:       r.CreatedAt,
	}, nil
}

func toAPIDispute(d domain.Dispute) orderapi.Dispute {
	return orderapi.Dispute{
		ID:        id.Of[id.RefundDispute](d.ID),
		RefundID:  id.Of[id.Refund](d.RefundID),
		Round:     d.Round,
		OpenedBy:  id.Of[id.Account](d.OpenedBy),
		Status:    d.Status,
		Reason:    d.Reason,
		Note:      d.Note,
		CreatedAt: d.CreatedAt,
		RuledAt:   d.RuledAt,
	}
}

// saveRefund writes a non-terminal transition and starts the clock the new status carries.
func (s *Service) saveRefund(ctx context.Context, r domain.Refund) error {
	if err := s.repo.SaveRefund(ctx, r); err != nil {
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

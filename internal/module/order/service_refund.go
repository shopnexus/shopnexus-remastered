package order

import (
	"context"
	"fmt"

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
	if req.Role == "seller" {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListRefunds(ctx, filter)
	if err != nil {
		return orderapi.RefundPage{}, fmt.Errorf("list refunds: %w", err)
	}
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	out := make([]orderapi.Refund, 0, len(rows))
	for _, r := range rows {
		view, err := s.refundView(ctx, r)
		if err != nil {
			return orderapi.RefundPage{}, err
		}
		out = append(out, view)
	}
	page := orderapi.RefundPage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		page.Meta.NextCursor = formatCursor(rows[len(rows)-1].CreatedAt)
	}
	return page, nil
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
	if r.Status != domain.RefundAwaitingSeller {
		return domain.ErrRefundSettled
	}
	if err := r.Rule(false); err != nil {
		// Rule only applies to a disputed case; withdrawing is its own transition.
		r.Status = domain.RefundRejected
		r.DeadlineAt = nil
	}
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return fmt.Errorf("save refund: %w", err)
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
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, fmt.Errorf("save refund: %w", err)
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
	if err := r.Accept(); err != nil {
		return orderapi.Refund{}, err
	}
	// The return leg, buyer to seller. There is no leg back: a seller who wins round one was
	// never sent anything.
	transport, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		return orderapi.Refund{}, fmt.Errorf("find transport: %w", err)
	}
	returnID, err := s.repo.InsertTransport(ctx, transport.Option)
	if err != nil {
		return orderapi.Refund{}, fmt.Errorf("open return transport: %w", err)
	}
	if err := r.StartReturn(returnID); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, fmt.Errorf("save refund: %w", err)
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
	if err := r.Reject(req.Reason); err != nil {
		return orderapi.Refund{}, err
	}
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return orderapi.Refund{}, fmt.Errorf("save refund: %w", err)
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
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return orderapi.Dispute{}, fmt.Errorf("save refund: %w", err)
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
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	out := make([]orderapi.Dispute, 0, len(rows))
	for _, d := range rows {
		out = append(out, toAPIDispute(d))
	}
	page := orderapi.DisputePage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		page.Meta.NextCursor = formatCursor(rows[len(rows)-1].CreatedAt)
	}
	return page, nil
}

// AdminRuleDispute applies the verdict to the round and to the refund behind it. A buyer who
// wins round one gets the goods collected and then the money; one who wins round two gets
// the money, because the goods are already back.
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
	settled := false
	if err := r.Rule(req.BuyerWins); err != nil {
		return orderapi.Dispute{}, err
	}
	settled = r.Status == domain.RefundAccepted
	if err := s.repo.SaveDispute(ctx, d); err != nil {
		return orderapi.Dispute{}, fmt.Errorf("save dispute: %w", err)
	}
	if err := s.repo.SaveRefund(ctx, r); err != nil {
		return orderapi.Dispute{}, fmt.Errorf("save refund: %w", err)
	}
	if settled {
		if err := s.payRefund(ctx, r); err != nil {
			return orderapi.Dispute{}, err
		}
	}
	return toAPIDispute(d), nil
}

// payRefund sends the escrow back and closes the order. Keyed on the order, so a verdict
// applied twice cannot pay the buyer twice.
func (s *Service) payRefund(ctx context.Context, r domain.Refund) error {
	o, err := s.repo.FindOrder(ctx, r.OrderID)
	if err != nil {
		return fmt.Errorf("find order: %w", err)
	}
	if err := s.refundEscrow(ctx, o, "refund"); err != nil {
		return err
	}
	if err := o.Cancel(false); err == nil {
		if err := s.repo.SaveOrder(ctx, o); err != nil {
			s.log.Error("close refunded order", "order_id", o.ID, "err", err)
		}
	}
	s.releaseOrderStock(ctx, o)
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

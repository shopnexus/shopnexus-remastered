package finance

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
	"shopnexus/internal/provider/payment"
	"shopnexus/internal/shared/id"
)

// ListSessions answers the caller's own money flows — both sides of them, since a
// payout is a session they received. Admin sees every account's.
func (s *Service) ListSessions(ctx context.Context, req financeapi.ListSessionsRequest) (financeapi.SessionPage, error) {
	filter := port.SessionFilter{
		AccountID: req.ActorID.Int64(),
		Kind:      req.Kind,
		Status:    req.Status,
		Offset:    offsetOf(req.Page, req.Limit),
		Limit:     req.Limit,
	}
	// One side of the money, when asked for: a payout and a refund are received, so they
	// answer to `payee` and not to the same list as a checkout.
	switch req.Role {
	case "payer":
		filter.PayerID = req.ActorID.Int64()
	case "payee":
		filter.PayeeID = req.ActorID.Int64()
	}
	if req.Admin {
		if err := s.requireAdmin(ctx, req.ActorID); err != nil {
			return financeapi.SessionPage{}, err
		}
		// Zero is the admin view: every session, whoever is party to it — unless staff
		// named one, which is the whole point of the filter.
		filter.AccountID = req.AccountID.Int64()
		filter.From = req.From
		filter.To = req.To
	}
	rows, total, err := s.repo.ListSessions(ctx, filter)
	if err != nil {
		return financeapi.SessionPage{}, fmt.Errorf("list payment sessions: %w", err)
	}
	out := make([]financeapi.Session, 0, len(rows))
	for _, session := range rows {
		// The outstanding balance needs the legs, so a page costs one query per row. A
		// list of a caller's sessions is short by construction; a batched read is the
		// change to make when that stops being true.
		outstanding, err := s.outstanding(ctx, session)
		if err != nil {
			return financeapi.SessionPage{}, err
		}
		out = append(out, toAPISession(session, outstanding))
	}
	return financeapi.SessionPage{
		Data: out,
		Meta: financeapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

func (s *Service) GetSession(ctx context.Context, req financeapi.GetSessionRequest) (financeapi.Session, error) {
	session, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return financeapi.Session{}, err
	}
	outstanding, err := s.outstanding(ctx, session)
	if err != nil {
		return financeapi.Session{}, err
	}
	return toAPISession(session, outstanding), nil
}

func (s *Service) ListSessionTransactions(ctx context.Context, req financeapi.GetSessionRequest) ([]financeapi.Transaction, error) {
	if _, err := s.party(ctx, req.ActorID, req.ID); err != nil {
		return nil, err
	}
	legs, err := s.repo.ListTransactions(ctx, req.ID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	out := make([]financeapi.Transaction, 0, len(legs))
	for _, leg := range legs {
		out = append(out, toAPITransaction(leg, ""))
	}
	return out, nil
}

// StartPayment opens a leg on one rail and hands back the gateway's redirect. The leg
// is pending: only the provider's webhook settles it, so this response is not a
// receipt however much it looks like one.
func (s *Service) StartPayment(ctx context.Context, req financeapi.StartPaymentRequest) (financeapi.Transaction, error) {
	session, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return financeapi.Transaction{}, err
	}
	// Only the payer tenders. The other side is a party to the session — they can read
	// it — but paying on somebody else's behalf is not a thing this API does.
	if session.FromID != req.ActorID.Int64() {
		return financeapi.Transaction{}, domain.ErrSessionNotPayable
	}
	// The row is what names the rail, so resolving it here is also the check that this deployment
	// still has the implementation it was written for.
	option, err := s.paymentOption(ctx, req.PaymentOption)
	if err != nil {
		return financeapi.Transaction{}, err
	}
	rail, err := s.rails.Client(option.Provider)
	if err != nil {
		s.log.Error("tendered rail has no registered provider",
			"option", option.ID, "provider", option.Provider, "err", err)
		return financeapi.Transaction{}, common.ErrOptionProviderUnknown
	}
	if err := s.checkReturnURL(req.ReturnURL); err != nil {
		return financeapi.Transaction{}, err
	}
	outstanding, err := s.outstanding(ctx, session)
	if err != nil {
		return financeapi.Transaction{}, err
	}
	amount := outstanding
	if req.Amount != nil {
		amount = *req.Amount
	}
	if amount <= 0 || amount > outstanding {
		return financeapi.Transaction{}, domain.ErrChargeAmountInvalid
	}
	if err := session.Charge(time.Now()); err != nil {
		return financeapi.Transaction{}, err
	}

	legID, err := s.repo.NextTransactionID(ctx)
	if err != nil {
		return financeapi.Transaction{}, fmt.Errorf("allocate transaction id: %w", err)
	}
	leg, err := domain.NewCharge(legID, session.ID, req.PaymentOption, session.Currency, amount, nil)
	if err != nil {
		return financeapi.Transaction{}, err
	}
	// The gateway is called before the row exists, which is why the id was allocated
	// first: the reference it is handed has to be the one the webhook will name.
	charge, err := rail.Charge(ctx, payment.ChargeParams{
		RefID:       id.Of[id.Transaction](legID).String(),
		Option:      req.PaymentOption,
		Amount:      amount,
		Currency:    session.Currency,
		Description: session.Note,
		ReturnURL:   req.ReturnURL,
	})
	if err != nil {
		return financeapi.Transaction{}, fmt.Errorf("charge payment rail: %w", err)
	}
	if charge.ProviderID != "" {
		leg.ProviderRef = &charge.ProviderID
	}
	if err := s.repo.InsertTransaction(ctx, &leg); err != nil {
		return financeapi.Transaction{}, fmt.Errorf("insert transaction: %w", err)
	}
	if err := s.repo.SaveSession(ctx, session, []string{domain.StatusPending}); err != nil {
		return financeapi.Transaction{}, fmt.Errorf("save payment session: %w", err)
	}
	// A direct-debit rail answers final on the spot, with no webhook to wait for.
	if charge.Status == payment.StatusSuccess || charge.Status == payment.StatusFailed {
		if err := s.settle(ctx, leg, charge.Status, charge.ProviderID, ""); err != nil {
			return financeapi.Transaction{}, err
		}
		leg, err = s.repo.FindTransactionByID(ctx, leg.ID)
		if err != nil {
			return financeapi.Transaction{}, fmt.Errorf("find transaction: %w", err)
		}
	}
	return toAPITransaction(leg, charge.RedirectURL), nil
}

// CancelSession is the payer walking away before the money moves. A paid session is
// refunded instead, which is the order module's flow and not a cancellation.
func (s *Service) CancelSession(ctx context.Context, req financeapi.GetSessionRequest) (financeapi.Session, error) {
	session, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return financeapi.Session{}, err
	}
	if session.FromID != req.ActorID.Int64() {
		return financeapi.Session{}, domain.ErrSessionNotPayable
	}
	// A withdrawal names its requester as the payer, so without this it could be cancelled
	// here — where nothing returns the debit — instead of through the route that does.
	if !session.RailPayable() {
		return financeapi.Session{}, domain.ErrSessionKindNotCancellable
	}
	if err := session.Cancel(); err != nil {
		return financeapi.Session{}, err
	}
	from := []string{domain.StatusPending, domain.StatusProcessing}
	if err := s.repo.SaveSession(ctx, session, from); err != nil {
		return financeapi.Session{}, fmt.Errorf("save payment session: %w", err)
	}
	s.publishCancelled(ctx, session)
	return toAPISession(session, 0), nil
}

// Settle is what a provider's notification does: it finds the leg by the reference the
// gateway was given, settles it, and when the session is covered publishes the fact
// the rest of the platform waits for.
//
// Not a route — the webhook is the provider's own, mounted by its client — so it takes
// raw values rather than a request DTO.
func (s *Service) Settle(ctx context.Context, notification payment.Notification) error {
	legID, err := id.Parse[id.Transaction](notification.RefID)
	if err != nil {
		return fmt.Errorf("parse notification reference: %w", err)
	}
	// By id, not by the provider's own reference: that ref is unique only per rail, so a
	// string two providers both use would settle the wrong leg. The id is ours, and it is the
	// reference the gateway was handed, so a notification always carries it.
	leg, err := s.repo.FindTransactionByID(ctx, legID.Int64())
	if err != nil {
		return fmt.Errorf("find transaction: %w", err)
	}
	status := payment.StatusFailed
	if notification.Status == payment.StatusSuccess {
		status = payment.StatusSuccess
	}
	return s.settle(ctx, leg, status, notification.ProviderTxID, string(notification.Status))
}

// settle books one leg's outcome and, when the session is fully covered, marks it paid
// and publishes it. The order module is subscribed to that event: the money landing is
// what creates an order, and nobody presses a button in between.
func (s *Service) settle(ctx context.Context, leg domain.Transaction, status payment.Status, providerRef, note string) error {
	domainStatus := domain.StatusFailed
	if status == payment.StatusSuccess {
		domainStatus = domain.StatusSuccess
	}
	if err := leg.Settle(domainStatus, providerRef, note); err != nil {
		// Already settled: a provider retries until it gets a 200, so a redelivery is
		// expected and is not an error to the caller.
		if errors.Is(err, domain.ErrTransactionSettled) {
			return nil
		}
		return err
	}
	if err := s.repo.SaveTransaction(ctx, leg); err != nil {
		if errors.Is(err, domain.ErrTransactionSettled) {
			return nil
		}
		return fmt.Errorf("save transaction: %w", err)
	}

	session, err := s.repo.FindSessionByID(ctx, leg.SessionID)
	if err != nil {
		return fmt.Errorf("find payment session: %w", err)
	}
	if domainStatus == domain.StatusFailed {
		// One failed rail does not fail the session: the payer may tender another, which
		// needs the session back on the shelf — Charge refuses a processing one, so without
		// this the split-tender the contract describes would be unreachable.
		if err := session.ReopenForRetry(time.Now()); err != nil {
			// Already settled or expired: there is nothing to tender against.
			return nil
		}
		from := []string{domain.StatusProcessing}
		if err := s.repo.SaveSession(ctx, session, from); err != nil && !errors.Is(err, domain.ErrSessionSettled) {
			return fmt.Errorf("reopen payment session: %w", err)
		}
		return nil
	}
	outstanding, err := s.outstanding(ctx, session)
	if err != nil {
		return err
	}
	if outstanding > 0 {
		return nil
	}
	// The rail money becomes a balance before the session is called paid. Without this the
	// money is collected and recorded nowhere: order's escrow hold debits the buyer's wallet,
	// which would be empty, so the hold would be refused and the sale would have no escrow
	// behind it. Keyed on the session, so a redelivered webhook credits once.
	if err := s.creditRail(ctx, session); err != nil {
		return err
	}
	if err := session.MarkPaid(time.Now()); err != nil {
		if errors.Is(err, domain.ErrSessionSettled) {
			return nil
		}
		return err
	}
	from := []string{domain.StatusPending, domain.StatusProcessing}
	if err := s.repo.SaveSession(ctx, session, from); err != nil {
		if errors.Is(err, domain.ErrSessionSettled) {
			// Another delivery of the same notification got there first.
			return nil
		}
		return fmt.Errorf("save payment session: %w", err)
	}
	s.publishPaid(ctx, session)
	return nil
}

// creditRail turns collected rail money into a spendable balance for whoever paid it. Only a
// checkout: a payout or a withdrawal moves money the other way and was already debited when it
// was asked for.
func (s *Service) creditRail(ctx context.Context, session domain.Session) error {
	if !session.RailPayable() || session.FromID == 0 {
		return nil
	}
	ref := domain.SessionRef(session.ID)
	legs := []port.Leg{{
		AccountID: session.FromID, Currency: session.Currency,
		Transfer: domain.Credit(domain.WalletKindTopup, session.TotalAmount, ref,
			fmt.Sprintf("session:%d:topup", session.ID), session.Note),
	}}
	if _, err := s.repo.Move(ctx, legs); err != nil {
		if errors.Is(err, domain.ErrMovementAlreadyPosted) {
			// A redelivered notification: the balance is already there.
			return nil
		}
		return fmt.Errorf("credit rail payment: %w", err)
	}
	return nil
}

// publishPaid announces a settled session. Best-effort on purpose: the money has
// already moved and the write has committed, so a bus that is down must not turn a
// successful payment into an error the provider will retry.
func (s *Service) publishPaid(ctx context.Context, session domain.Session) {
	event := SessionPaid{
		SessionID: session.ID,
		Kind:      session.Kind,
		FromID:    session.FromID,
		ToID:      session.ToID,
		Currency:  session.Currency,
		Amount:    session.TotalAmount,
		Data:      jsontext.Value(session.Data),
	}
	if err := publishSessionPaid(ctx, s.bus, event); err != nil {
		s.log.Error("publish session paid failed", "session_id", session.ID, "err", err)
	}
}

// publishCancelled announces a session the payer dropped. Best-effort for the same reason
// as publishPaid: the write has committed and the caller was told it worked, so a bus that
// is down must not turn a successful cancellation into an error.
//
// What the subscriber does with it is not optional, though — a lost message leaves the
// reservation held. The order module's expiry sweep is the backstop, exactly as it is for
// the checkout nobody ever came back to.
func (s *Service) publishCancelled(ctx context.Context, session domain.Session) {
	event := SessionCancelled{
		SessionID: session.ID,
		Kind:      session.Kind,
		FromID:    session.FromID,
		ToID:      session.ToID,
	}
	if err := publishSessionCancelled(ctx, s.bus, event); err != nil {
		s.log.Error("publish session cancelled failed", "session_id", session.ID, "err", err)
	}
}

// outstanding is the total less what has settled on a rail. Computed rather than
// stored: a cached copy would be a second fact to keep in step with every leg, and the
// legs are the ones that decide whether the money arrived.
func (s *Service) outstanding(ctx context.Context, session domain.Session) (int64, error) {
	legs, err := s.repo.ListTransactions(ctx, session.ID)
	if err != nil {
		return 0, fmt.Errorf("list transactions: %w", err)
	}
	settled := int64(0)
	for _, leg := range legs {
		if leg.Status == domain.StatusSuccess {
			// Signed, so a reversal subtracts itself.
			settled += leg.Amount
		}
	}
	if settled >= session.TotalAmount {
		return 0, nil
	}
	return session.TotalAmount - settled, nil
}

// party reads a session the caller is allowed to see. Somebody else's money flow is
// not found rather than forbidden — it is not theirs to know about.
func (s *Service) party(ctx context.Context, actorID id.ID[id.Account], sessionID id.ID[id.PaymentSession]) (domain.Session, error) {
	session, err := s.repo.FindSessionByID(ctx, sessionID.Int64())
	if err != nil {
		return domain.Session{}, fmt.Errorf("find payment session: %w", err)
	}
	if !session.Involves(actorID.Int64()) {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	return session, nil
}

package finance

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
	"shopnexus/internal/shared/id"
)

// withdrawalData is what a cash-out session carries in its `data`: where the money is
// going and what an admin decided. It lives in the session rather than in a table of
// its own, because a withdrawal *is* a money flow — the same lifecycle a checkout has.
type withdrawalData struct {
	BankAccountID int64      `json:"bank_account_id"`
	Reason        string     `json:"reason,omitempty"`
	ProviderRef   string     `json:"provider_ref,omitempty"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
}

// CreateWithdrawal debits the available balance now and opens the request for review.
// Debiting up front is the point: the money is out of reach the moment it is asked for,
// so the same balance cannot be withdrawn twice while a human works the queue.
func (s *Service) CreateWithdrawal(ctx context.Context, req financeapi.CreateWithdrawalRequest) (financeapi.Withdrawal, error) {
	payee, err := s.repo.FindBankAccount(ctx, req.BankAccountID.Int64(), req.ActorID.Int64())
	if err != nil {
		return financeapi.Withdrawal{}, fmt.Errorf("find bank account: %w", err)
	}
	// Real money leaves the platform here, so the payee has to be who they say they
	// are. The flag is the account module's, and the same one that gates selling.
	me, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: req.ActorID})
	if err != nil {
		return financeapi.Withdrawal{}, fmt.Errorf("read payee: %w", err)
	}
	if !me.IdentityVerified {
		return financeapi.Withdrawal{}, domain.ErrPayeeUnverified
	}

	sessionID, err := s.repo.NextSessionID(ctx)
	if err != nil {
		return financeapi.Withdrawal{}, fmt.Errorf("allocate session id: %w", err)
	}
	data, err := json.Marshal(withdrawalData{BankAccountID: payee.ID})
	if err != nil {
		return financeapi.Withdrawal{}, fmt.Errorf("encode withdrawal data: %w", err)
	}
	session, err := domain.NewSession(sessionID, domain.KindWithdrawal, req.ActorID.Int64(), 0,
		"withdrawal", req.Currency, req.Amount, data, withdrawalTTL)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	// The request and the debit are one fact, written in one transaction. Apart, a crash
	// between them strands a pending cash-out with no debit behind it — and an admin
	// approving that one sends real money for a balance that was never reduced. The debit
	// carries the session as its idempotency key, so a retried create cannot debit twice.
	ref := domain.SessionRef(session.ID)
	debit := port.Leg{
		AccountID: req.ActorID.Int64(), Currency: req.Currency,
		Transfer: domain.Debit(domain.WalletKindWithdrawal, req.Amount, ref,
			fmt.Sprintf("withdrawal:%d", session.ID), "withdrawal requested"),
	}
	if err := s.repo.InsertWithdrawal(ctx, &session, debit); err != nil {
		return financeapi.Withdrawal{}, fmt.Errorf("open withdrawal: %w", err)
	}
	return toAPIWithdrawal(session)
}

func (s *Service) ListWithdrawals(ctx context.Context, req financeapi.ListWithdrawalsRequest) (financeapi.WithdrawalPage, error) {
	filter := port.SessionFilter{
		AccountID: req.ActorID.Int64(),
		Kind:      domain.KindWithdrawal,
		Status:    req.Status,
		Offset:    offsetOf(req.Page, req.Limit),
		Limit:     req.Limit,
	}
	if req.Admin {
		if err := s.requireAdmin(ctx, req.ActorID); err != nil {
			return financeapi.WithdrawalPage{}, err
		}
		filter.AccountID = 0
	}
	rows, total, err := s.repo.ListSessions(ctx, filter)
	if err != nil {
		return financeapi.WithdrawalPage{}, fmt.Errorf("list withdrawals: %w", err)
	}
	out := make([]financeapi.Withdrawal, 0, len(rows))
	for _, session := range rows {
		w, err := toAPIWithdrawal(session)
		if err != nil {
			return financeapi.WithdrawalPage{}, err
		}
		out = append(out, w)
	}
	return financeapi.WithdrawalPage{
		Data: out,
		Meta: financeapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

func (s *Service) GetWithdrawal(ctx context.Context, req financeapi.WithdrawalRequest) (financeapi.Withdrawal, error) {
	session, err := s.withdrawal(ctx, req.ActorID, req.ID, false)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	return toAPIWithdrawal(session)
}

// CancelWithdrawal is the requester changing their mind before an admin gets to it. The
// debit is returned in the same call: money taken for a request that no longer exists
// would be money the platform simply kept.
func (s *Service) CancelWithdrawal(ctx context.Context, req financeapi.WithdrawalRequest) error {
	session, err := s.withdrawal(ctx, req.ActorID, req.ID, false)
	if err != nil {
		return err
	}
	if err := session.Cancel(); err != nil {
		return err
	}
	// Guarded by the status it moves from: a cancellation racing an admin's decision must
	// lose rather than both landing, or the money would be returned and the payout recorded.
	if err := s.repo.SaveSession(ctx, session, liveWithdrawal); err != nil {
		return fmt.Errorf("save withdrawal: %w", err)
	}
	return s.returnWithdrawal(ctx, session, "withdrawal cancelled")
}

// AdminApproveWithdrawal is the money leaving. The debit already happened when the
// request was made, so approval records the transfer rather than moving anything —
// which is why a bank reference is worth keeping: it is the only handle on the payment
// once it is outside the platform.
func (s *Service) AdminApproveWithdrawal(ctx context.Context, req financeapi.ResolveWithdrawalRequest) (financeapi.Withdrawal, error) {
	session, err := s.withdrawal(ctx, req.ActorID, req.ID, true)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	if err := session.MarkPaid(time.Now()); err != nil {
		return financeapi.Withdrawal{}, err
	}
	data, err := decodeWithdrawal(session)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	data.ProviderRef, data.Reason = req.ProviderRef, req.Reason
	data.ResolvedAt = new(time.Now())
	if err := s.saveWithdrawalData(ctx, &session, data); err != nil {
		return financeapi.Withdrawal{}, err
	}
	return toAPIWithdrawal(session)
}

// AdminRejectWithdrawal gives the money back. The reason is not optional in practice —
// somebody's cash-out did not happen and they are owed the why — so it is recorded on
// the session and in the ledger note.
func (s *Service) AdminRejectWithdrawal(ctx context.Context, req financeapi.ResolveWithdrawalRequest) (financeapi.Withdrawal, error) {
	session, err := s.withdrawal(ctx, req.ActorID, req.ID, true)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	if req.Reason == "" {
		return financeapi.Withdrawal{}, domain.ErrRejectionNeedsReason
	}
	if err := session.MarkFailed(); err != nil {
		return financeapi.Withdrawal{}, err
	}
	data, err := decodeWithdrawal(session)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	data.Reason = req.Reason
	data.ResolvedAt = new(time.Now())
	if err := s.saveWithdrawalData(ctx, &session, data); err != nil {
		return financeapi.Withdrawal{}, err
	}
	if err := s.returnWithdrawal(ctx, session, "withdrawal rejected: "+req.Reason); err != nil {
		return financeapi.Withdrawal{}, err
	}
	return toAPIWithdrawal(session)
}

// returnWithdrawal credits the debited amount back. Keyed on the session and the
// direction, so a cancel followed by a rejected retry cannot refund twice.
func (s *Service) returnWithdrawal(ctx context.Context, session domain.Session, note string) error {
	ref := domain.SessionRef(session.ID)
	legs := []port.Leg{{
		AccountID: session.FromID, Currency: session.Currency,
		Transfer: domain.Credit(domain.WalletKindWithdrawal, session.TotalAmount, ref,
			fmt.Sprintf("withdrawal:%d:return", session.ID), note),
	}}
	if _, err := s.repo.Move(ctx, legs); err != nil {
		return fmt.Errorf("return withdrawal: %w", err)
	}
	return nil
}

// withdrawal reads a cash-out the caller may act on. An admin may act on anybody's; a
// requester only on their own, and somebody else's is not found rather than forbidden.
func (s *Service) withdrawal(ctx context.Context, actorID id.ID[id.Account], sessionID id.ID[id.PaymentSession], admin bool) (domain.Session, error) {
	if admin {
		if err := s.requireAdmin(ctx, actorID); err != nil {
			return domain.Session{}, err
		}
	}
	session, err := s.repo.FindSessionByID(ctx, sessionID.Int64())
	if err != nil {
		return domain.Session{}, fmt.Errorf("find withdrawal: %w", err)
	}
	if session.Kind != domain.KindWithdrawal {
		return domain.Session{}, domain.ErrWithdrawalNotFound
	}
	if !admin && session.FromID != actorID.Int64() {
		return domain.Session{}, domain.ErrWithdrawalNotFound
	}
	return session, nil
}

func (s *Service) saveWithdrawalData(ctx context.Context, session *domain.Session, data withdrawalData) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode withdrawal data: %w", err)
	}
	session.Data = encoded
	if err := s.repo.SaveSession(ctx, *session, liveWithdrawal); err != nil {
		return fmt.Errorf("save withdrawal: %w", err)
	}
	return nil
}

// liveWithdrawal is the set a cash-out can still be resolved from. Every write here names it,
// so an approval and a rejection racing on one request cannot both land — which would return
// the money to the wallet and record the payout as sent.
var liveWithdrawal = []string{domain.StatusPending, domain.StatusProcessing}

func decodeWithdrawal(session domain.Session) (withdrawalData, error) {
	var data withdrawalData
	if len(session.Data) == 0 {
		return data, nil
	}
	if err := json.Unmarshal(session.Data, &data); err != nil {
		return withdrawalData{}, fmt.Errorf("decode withdrawal data: %w", err)
	}
	return data, nil
}

func toAPIWithdrawal(session domain.Session) (financeapi.Withdrawal, error) {
	data, err := decodeWithdrawal(session)
	if err != nil {
		return financeapi.Withdrawal{}, err
	}
	return financeapi.Withdrawal{
		ID:            id.Of[id.PaymentSession](session.ID),
		Status:        session.Status,
		Currency:      session.Currency,
		Amount:        session.TotalAmount,
		BankAccountID: id.Of[id.BankAccount](data.BankAccountID),
		Reason:        data.Reason,
		CreatedAt:     session.CreatedAt,
		ResolvedAt:    data.ResolvedAt,
	}, nil
}

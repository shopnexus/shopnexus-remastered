package finance

import (
	"context"
	"errors"
	"fmt"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
)

// ListWallets answers every balance the caller holds. A wallet is not registered: it
// exists the first time money arrives in that currency, so an account with no history
// answers an empty list rather than a 404.
func (s *Service) ListWallets(ctx context.Context, req financeapi.ListWalletsRequest) ([]financeapi.Wallet, error) {
	rows, err := s.repo.ListWallets(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	out := make([]financeapi.Wallet, 0, len(rows))
	for _, w := range rows {
		out = append(out, toAPIWallet(w))
	}
	return out, nil
}

// GetWallet reads one balance. Another account's is admin-only: a balance is the
// closest thing this platform has to a bank statement.
func (s *Service) GetWallet(ctx context.Context, req financeapi.GetWalletRequest) (financeapi.Wallet, error) {
	if req.AccountID != req.ActorID {
		if err := s.requireAdmin(ctx, req.ActorID); err != nil {
			return financeapi.Wallet{}, err
		}
	}
	w, err := s.repo.FindWallet(ctx, req.AccountID.Int64(), req.Currency)
	if err != nil {
		return financeapi.Wallet{}, fmt.Errorf("find wallet: %w", err)
	}
	return toAPIWallet(w), nil
}

// ListWalletMovements is the statement: newest first, with the balance each movement
// produced, so a reader never has to replay the ledger to see where they stood.
func (s *Service) ListWalletMovements(ctx context.Context, req financeapi.ListMovementsRequest) (financeapi.WalletMovementPage, error) {
	rows, total, err := s.repo.ListMovements(ctx, port.MovementFilter{
		AccountID: req.ActorID.Int64(),
		Currency:  req.Currency,
		Kind:      req.Kind,
		Offset:    offsetOf(req.Page, req.Limit),
		Limit:     req.Limit,
	})
	if err != nil {
		return financeapi.WalletMovementPage{}, fmt.Errorf("list wallet movements: %w", err)
	}
	out := make([]financeapi.WalletMovement, 0, len(rows))
	for _, m := range rows {
		out = append(out, toAPIMovement(m))
	}
	return financeapi.WalletMovementPage{
		Data: out,
		Meta: financeapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// AdminListWallets is every currency an account holds. An admin surface: a support agent
// looking at a balance dispute does not know which currency it is in.
func (s *Service) AdminListWallets(ctx context.Context, req financeapi.AdminListWalletsRequest) ([]financeapi.Wallet, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListWallets(ctx, req.AccountID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	out := make([]financeapi.Wallet, 0, len(rows))
	for _, w := range rows {
		out = append(out, toAPIWallet(w))
	}
	return out, nil
}

// AdminAdjustWallet is the correction of last resort — a support credit, a mistake
// being unwound. The only movement with no order or session behind it, which is why
// the reason is mandatory: the ledger note is the entire explanation an audit gets.
func (s *Service) AdminAdjustWallet(ctx context.Context, req financeapi.AdjustWalletRequest) (financeapi.Wallet, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return financeapi.Wallet{}, err
	}
	// Validated here and not only at the handler: the idempotency key is what stops a second
	// credit, so a caller that omits it must be refused wherever it came from.
	if err := s.v.Struct(req); err != nil {
		return financeapi.Wallet{}, err
	}
	transfer := domain.Adjust(req.AvailableDelta, req.HeldDelta,
		"adjustment:"+req.IdempotencyKey, req.Reason)
	_, err := s.repo.Move(ctx, []port.Leg{{
		AccountID: req.AccountID.Int64(), Currency: req.Currency, Transfer: transfer,
	}})
	// A key used before is the correction already applied, so this answers the wallet as it
	// stands: a retried request is the state the admin asked for, not a second credit.
	if err != nil && !errors.Is(err, domain.ErrMovementAlreadyPosted) {
		return financeapi.Wallet{}, fmt.Errorf("adjust wallet: %w", err)
	}
	w, err := s.repo.FindWallet(ctx, req.AccountID.Int64(), req.Currency)
	if err != nil {
		return financeapi.Wallet{}, fmt.Errorf("find wallet: %w", err)
	}
	return toAPIWallet(w), nil
}

// --- what order calls ---

// OpenCheckout creates the session a purchase is paid through. Finance owns the money
// from here: the total, the expiry, the rails and the event that says it landed.
func (s *Service) OpenCheckout(ctx context.Context, req financeapi.OpenCheckoutRequest) (financeapi.Session, error) {
	if err := s.v.Struct(req); err != nil {
		return financeapi.Session{}, fmt.Errorf("validate checkout request: %w", err)
	}
	sessionID, err := s.repo.NextSessionID(ctx)
	if err != nil {
		return financeapi.Session{}, fmt.Errorf("allocate session id: %w", err)
	}
	session, err := domain.NewSession(sessionID, domain.KindBuyerCheckout,
		req.BuyerID.Int64(), req.SellerID.Int64(), req.Note, req.Currency, req.Total,
		req.Data, sessionTTL)
	if err != nil {
		return financeapi.Session{}, err
	}
	if err := s.repo.InsertSession(ctx, &session); err != nil {
		return financeapi.Session{}, fmt.Errorf("insert payment session: %w", err)
	}
	// A session nobody has tendered yet: the whole total is outstanding and there is no
	// gateway page to go back to.
	return toAPISession(session, tender{Outstanding: session.TotalAmount}), nil
}

// HoldEscrow is what a paid order does to the money: it leaves the buyer and lands in
// the seller's held balance, where neither side can spend it. Two legs in one
// transaction — the buyer's debit and the seller's hold — because money that left one
// wallet without arriving in the other is the failure this module exists to prevent.
func (s *Service) HoldEscrow(ctx context.Context, req financeapi.EscrowRequest) error {
	if err := s.v.Struct(req); err != nil {
		return fmt.Errorf("validate escrow request: %w", err)
	}
	ref := domain.OrderRef(req.OrderID.Int64())
	legs := []port.Leg{
		{
			AccountID: req.BuyerID.Int64(), Currency: req.Currency,
			Transfer: domain.Debit(domain.WalletKindEscrowHold, req.Amount, ref,
				req.IdempotencyKey+":buyer", "checkout debit"),
		},
		{
			AccountID: req.SellerID.Int64(), Currency: req.Currency,
			Transfer: domain.Transfer{
				Kind: domain.WalletKindEscrowHold, HeldDelta: req.Amount,
				RefType: ref.Type(), RefID: ref.ID(),
				IdempotencyKey: new(req.IdempotencyKey + ":seller"),
				Note:           "escrow hold",
			},
		},
	}
	// The delivery the buyer paid for. A third leg in the same movement rather than a second
	// call: the buyer was credited the whole session, and a fee collected by a write that could
	// fail on its own would leave the courier's money sitting in the buyer's wallet.
	if req.ShippingFee > 0 {
		legs = append(legs, port.Leg{
			AccountID: req.BuyerID.Int64(), Currency: req.Currency,
			Transfer: domain.Debit(domain.WalletKindFee, req.ShippingFee, ref,
				req.IdempotencyKey+":shipping", "shipping fee"),
		})
	}
	if _, err := s.repo.Move(ctx, legs); err != nil {
		return fmt.Errorf("hold escrow: %w", err)
	}
	return nil
}

// ReleaseEscrow is the payout: the seller's held money becomes spendable. One leg,
// because the money is already in the right wallet — only which half of it moves.
func (s *Service) ReleaseEscrow(ctx context.Context, req financeapi.EscrowRequest) error {
	if err := s.v.Struct(req); err != nil {
		return fmt.Errorf("validate escrow request: %w", err)
	}
	ref := domain.OrderRef(req.OrderID.Int64())
	legs := []port.Leg{{
		AccountID: req.SellerID.Int64(), Currency: req.Currency,
		Transfer: domain.Release(req.Amount, ref, req.IdempotencyKey, "escrow release"),
	}}
	if _, err := s.repo.Move(ctx, legs); err != nil {
		return fmt.Errorf("release escrow: %w", err)
	}
	return nil
}

// RefundEscrow sends the held money back to the buyer instead: out of the seller's
// held balance and into the buyer's available one, in one transaction for the same
// reason the hold was.
func (s *Service) RefundEscrow(ctx context.Context, req financeapi.EscrowRequest) error {
	if err := s.v.Struct(req); err != nil {
		return fmt.Errorf("validate escrow request: %w", err)
	}
	ref := domain.OrderRef(req.OrderID.Int64())
	legs := []port.Leg{
		{
			AccountID: req.SellerID.Int64(), Currency: req.Currency,
			Transfer: domain.Transfer{
				Kind: domain.WalletKindRefund, HeldDelta: -req.Amount,
				RefType: ref.Type(), RefID: ref.ID(),
				IdempotencyKey: new(req.IdempotencyKey + ":seller"),
				Note:           "refund released from escrow",
			},
		},
		{
			AccountID: req.BuyerID.Int64(), Currency: req.Currency,
			Transfer: domain.Credit(domain.WalletKindRefund, req.Amount, ref,
				req.IdempotencyKey+":buyer", "refund returned"),
		},
	}
	// Carriage the buyer paid for and never got. The caller decides whether that is the case —
	// it knows whether the parcel moved — and a fee that was earned is simply not sent here.
	if req.ShippingFee > 0 {
		legs = append(legs, port.Leg{
			AccountID: req.BuyerID.Int64(), Currency: req.Currency,
			Transfer: domain.Credit(domain.WalletKindRefund, req.ShippingFee, ref,
				req.IdempotencyKey+":shipping", "shipping fee returned"),
		})
	}
	if _, err := s.repo.Move(ctx, legs); err != nil {
		return fmt.Errorf("refund escrow: %w", err)
	}
	return nil
}

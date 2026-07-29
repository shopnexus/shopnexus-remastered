// Package finance implements financeapi.Service.
package finance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
	"shopnexus/internal/shared/id"
)

// sessionTTL is how long a pending session stays payable before it auto-voids.
const sessionTTL = 15 * time.Minute

type Service struct {
	repo port.Repository
	log  *slog.Logger
}

func NewService(repo port.Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

var _ financeapi.Service = (*Service)(nil)

func (s *Service) CreateSession(ctx context.Context, req financeapi.CreateSessionRequest) (financeapi.Session, error) {
	// The id is allocated before the INSERT, not by DEFAULT: a provider may need
	// it before the row exists (gateway redirect URLs embed it).
	sessionID, err := s.repo.NextSessionID(ctx)
	if err != nil {
		return financeapi.Session{}, fmt.Errorf("allocate session id: %w", err)
	}
	session, err := domain.NewSession(sessionID, req.Kind, req.FromID.Int64(), req.ToID.Int64(), req.Note,
		req.Currency, req.TotalAmount, req.Data, sessionTTL)
	if err != nil {
		return financeapi.Session{}, err
	}
	if err := s.repo.InsertSession(ctx, &session); err != nil {
		return financeapi.Session{}, fmt.Errorf("insert finance session: %w", err)
	}
	return toAPISession(session), nil
}

func (s *Service) GetSession(ctx context.Context, req financeapi.GetSessionRequest) (financeapi.Session, error) {
	session, err := s.repo.FindSessionByID(ctx, req.ID.Int64())
	if err != nil {
		return financeapi.Session{}, fmt.Errorf("find finance session: %w", err)
	}
	return toAPISession(session), nil
}

func (s *Service) GetWallet(ctx context.Context, req financeapi.GetWalletRequest) (financeapi.Wallet, error) {
	w, err := s.repo.FindWallet(ctx, req.AccountID.Int64(), req.Currency)
	if err != nil {
		return financeapi.Wallet{}, fmt.Errorf("find wallet: %w", err)
	}
	return financeapi.Wallet{
		AccountID:        id.Of[id.Account](w.AccountID),
		Currency:         w.Currency,
		AvailableBalance: w.AvailableBalance,
		HeldBalance:      w.HeldBalance,
	}, nil
}

func toAPISession(s domain.Session) financeapi.Session {
	return financeapi.Session{
		ID:          id.Of[id.PaymentSession](s.ID),
		Kind:        s.Kind,
		Status:      s.Status,
		Currency:    s.Currency,
		TotalAmount: s.TotalAmount,
		Note:        s.Note,
		CreatedAt:   s.CreatedAt,
		PaidAt:      s.PaidAt,
		ExpiredAt:   s.ExpiredAt,
	}
}

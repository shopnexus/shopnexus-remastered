// Package finance implements financeapi.Service — payment sessions, the wallet
// ledger, bank accounts, withdrawals and tax registrations.
//
// Every balance change goes through port.Move, which is one transaction per logical
// movement: this module owns all the money primitives so an escrow move cannot be
// half-applied.
package finance

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"time"

	"github.com/go-playground/validator/v10"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
	"shopnexus/internal/provider/payment"
	"shopnexus/internal/shared/id"
)

// sessionTTL is how long a pending session stays payable before the expiry job voids
// it. A checkout that sits open holds stock reserved, so it is deliberately short.
const sessionTTL = 15 * time.Minute

// withdrawalTTL is longer: a cash-out waits on a human, and an admin queue is not
// worked in fifteen minutes.
const withdrawalTTL = 30 * 24 * time.Hour

type Service struct {
	repo port.Repository
	// accounts answers the caller's role, and whether a payee's identity is verified —
	// both are rows in the account module's tables.
	accounts accountapi.Service
	// options is the payment rails registry: this module's own `option` rows, so a rail
	// nobody enabled cannot be tendered.
	options common.OptionStore
	// rails is every implementation money can move through. A tendered option row names one, so
	// two can be live at once and an admin can move a rail without a restart.
	rails *common.Registry[payment.Client]
	// returnURLHosts is the allowlist a payer's redirect target has to be in.
	returnURLHosts ReturnURLHosts
	bus            eventbus.Client
	v              *validator.Validate
	log            *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	options common.OptionStore,
	rails *common.Registry[payment.Client],
	returnURLHosts ReturnURLHosts,
	bus eventbus.Client,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{
		repo: repo, accounts: accounts, options: options, rails: rails,
		returnURLHosts: returnURLHosts, bus: bus, v: v, log: log,
	}
}

// SyncOptions writes the rows every registered rail says it owns. Called once at startup, so the
// list a payer picks from is the code that will serve it — and a rail no longer registered leaves
// no rows behind for somebody to tender against.
func (s *Service) SyncOptions(ctx context.Context) error {
	return s.rails.Each(func(provider string, client payment.Client) error {
		declared := client.Options()
		// No declared rows means the provider leaves them to the operator, which is not the same
		// as declaring none: reconciling here would delete rails somebody wrote by hand.
		if len(declared) == 0 {
			return nil
		}
		want := make([]common.Option, 0, len(declared))
		for _, o := range declared {
			want = append(want, common.Option{
				ID: o.ID, Name: o.Name, Description: o.Description,
				Priority: o.Priority, Category: common.CategoryPayment,
			})
		}
		return s.options.Reconcile(ctx, provider, want)
	})
}

// ReturnURLHosts is where a gateway may send a payer back. Its own type, not a bare []string:
// the fx graph is keyed by type, and "the list of strings" is not a thing to inject.
type ReturnURLHosts []string

// checkReturnURL refuses a redirect target that is not the platform's. A payer supplies this
// and a gateway sends them to it, so an unchecked one turns the checkout into an open redirect
// somebody else's phishing page can borrow this domain's credibility from.
func (s *Service) checkReturnURL(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return domain.ErrReturnURLNotAllowed
	}
	if slices.Contains(s.returnURLHosts, parsed.Host) {
		return nil
	}
	return domain.ErrReturnURLNotAllowed
}

var _ financeapi.Service = (*Service)(nil)

// requireAdmin asks the account module for the caller's role: it is a column in that
// module's table, so there is nowhere else to learn it.
func (s *Service) requireAdmin(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != accountapi.RoleAdmin {
		return domain.ErrAdminRequired
	}
	return nil
}

// paymentOption resolves a rail from this module's registry. A slug nobody enabled is
// refused here rather than handed to a gateway that has never heard of it.
func (s *Service) paymentOption(ctx context.Context, slug string) (common.Option, error) {
	options, err := s.options.ListEnabled(ctx, common.CategoryPayment)
	if err != nil {
		return common.Option{}, fmt.Errorf("list payment options: %w", err)
	}
	for _, o := range options {
		if o.ID == slug {
			return o, nil
		}
	}
	return common.Option{}, domain.ErrPaymentOptionUnknown
}

// offsetOf turns a 1-based page into an offset. Page and limit are validated at the
// DTO, so this needs no bounds of its own.
func offsetOf(page, limit int) int { return (page - 1) * limit }

func toAPISession(s domain.Session, outstanding int64) financeapi.Session {
	return financeapi.Session{
		ID:          id.Of[id.PaymentSession](s.ID),
		Kind:        s.Kind,
		Status:      s.Status,
		Currency:    s.Currency,
		TotalAmount: s.TotalAmount,
		Outstanding: outstanding,
		Note:        s.Note,
		Data:        rawOrEmpty(s.Data),
		CreatedAt:   s.CreatedAt,
		PaidAt:      s.PaidAt,
		ExpiredAt:   s.ExpiredAt,
	}
}

func toAPIWallet(w domain.Wallet) financeapi.Wallet {
	return financeapi.Wallet{
		AccountID:        id.Of[id.Account](w.AccountID),
		Currency:         w.Currency,
		AvailableBalance: w.AvailableBalance,
		HeldBalance:      w.HeldBalance,
		CreatedAt:        w.CreatedAt,
	}
}

func toAPITransaction(t domain.Transaction, checkoutURL string) financeapi.Transaction {
	out := financeapi.Transaction{
		ID:            id.Of[id.Transaction](t.ID),
		SessionID:     id.Of[id.PaymentSession](t.SessionID),
		Status:        t.Status,
		PaymentOption: t.PaymentOption,
		Amount:        t.Amount,
		Currency:      t.Currency,
		Note:          t.Note,
		CheckoutURL:   checkoutURL,
		CreatedAt:     t.CreatedAt,
		SettledAt:     t.SettledAt,
		ExpiredAt:     t.ExpiredAt,
	}
	if t.ReversesID != nil {
		out.ReversesID = new(id.Of[id.Transaction](*t.ReversesID))
	}
	if t.Error != nil {
		out.Error = *t.Error
	}
	return out
}

func toAPIMovement(m domain.Movement) financeapi.WalletMovement {
	out := financeapi.WalletMovement{
		Seq:            m.Seq,
		Currency:       m.Currency,
		Kind:           m.Kind,
		AvailableDelta: m.AvailableDelta,
		HeldDelta:      m.HeldDelta,
		AvailableAfter: m.AvailableAfter,
		HeldAfter:      m.HeldAfter,
		Note:           m.Note,
		CreatedAt:      m.CreatedAt,
	}
	// A ref is a polymorphic pointer, so it goes out as the opaque id of whatever kind
	// it names — the same shape a report's ref_id has.
	if m.RefType != nil && m.RefID != nil {
		out.RefType = *m.RefType
		switch *m.RefType {
		case domain.RefOrder:
			out.RefID = id.Of[id.Order](*m.RefID).String()
		case domain.RefPaymentSession:
			out.RefID = id.Of[id.PaymentSession](*m.RefID).String()
		}
	}
	return out
}

// rawOrEmpty is the JSON column as a DTO field: nil rather than the literal "null", so an absent
// context is an absent field instead of a string a client has to special-case.
func rawOrEmpty(b []byte) jsontext.Value {
	if len(b) == 0 {
		return nil
	}
	return jsontext.Value(b)
}

// ListOptions is the payment rails, and the only place a valid `payment_option` comes from: a client
// that hardcoded a slug broke the day an operator disabled that rail.
func (s *Service) ListOptions(ctx context.Context, req common.ListOptionsRequest) (common.OptionList, error) {
	if req.Admin {
		if err := s.requireAdmin(ctx, req.ActorID); err != nil {
			return common.OptionList{}, err
		}
	}
	return common.ListOptions(ctx, s.options, s.rails.Providers(), req)
}

// AdminSaveOption is an operator switching a rail off, renaming it, or moving it to another
// implementation. Admin, not moderator: this decides who the platform's money goes through.
func (s *Service) AdminSaveOption(ctx context.Context, req common.SaveOptionRequest) (common.OptionDTO, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return common.OptionDTO{}, err
	}
	if err := s.v.Struct(req); err != nil {
		return common.OptionDTO{}, err
	}
	return common.SaveOption(ctx, s.options, s.rails.Providers(), req)
}

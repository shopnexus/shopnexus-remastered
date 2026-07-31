package finance

import (
	"context"
	"fmt"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/shared/id"
)

// ListBankAccounts answers the caller's payout destinations, default first — which is
// the one a withdrawal form should preselect.
func (s *Service) ListBankAccounts(ctx context.Context, req financeapi.ListBankAccountsRequest) ([]financeapi.BankAccount, error) {
	rows, err := s.repo.ListBankAccounts(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list bank accounts: %w", err)
	}
	out := make([]financeapi.BankAccount, 0, len(rows))
	for _, b := range rows {
		out = append(out, toAPIBankAccount(b))
	}
	return out, nil
}

// CreateBankAccount registers a destination. The first one an account adds becomes the
// default whatever the request said: a payout form with nothing preselected is a
// mistake waiting to be made, and there is nothing to compete with.
func (s *Service) CreateBankAccount(ctx context.Context, req financeapi.CreateBankAccountRequest) (financeapi.BankAccount, error) {
	existing, err := s.repo.ListBankAccounts(ctx, req.ActorID.Int64())
	if err != nil {
		return financeapi.BankAccount{}, fmt.Errorf("list bank accounts: %w", err)
	}
	b, err := domain.NewBankAccount(req.ActorID.Int64(), req.BankCode, req.AccountNumber,
		req.AccountHolder, req.IsDefault || len(existing) == 0)
	if err != nil {
		return financeapi.BankAccount{}, err
	}
	if err := s.repo.InsertBankAccount(ctx, &b); err != nil {
		return financeapi.BankAccount{}, fmt.Errorf("insert bank account: %w", err)
	}
	return toAPIBankAccount(b), nil
}

// UpdateBankAccount only moves the default flag. A changed number is a different
// destination — a withdrawal already pointing at this row must not follow it — so
// that is a new registration and a delete, not an edit.
func (s *Service) UpdateBankAccount(ctx context.Context, req financeapi.UpdateBankAccountRequest) (financeapi.BankAccount, error) {
	b, err := s.repo.FindBankAccount(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return financeapi.BankAccount{}, fmt.Errorf("find bank account: %w", err)
	}
	b.IsDefault = req.IsDefault
	if err := s.repo.SaveBankAccount(ctx, b); err != nil {
		return financeapi.BankAccount{}, fmt.Errorf("save bank account: %w", err)
	}
	return toAPIBankAccount(b), nil
}

// DeleteBankAccount is a soft delete: a completed withdrawal names this row as where
// real money went, and financial history cannot lose its payee.
func (s *Service) DeleteBankAccount(ctx context.Context, req financeapi.DeleteBankAccountRequest) error {
	if _, err := s.repo.FindBankAccount(ctx, req.ID.Int64(), req.ActorID.Int64()); err != nil {
		return fmt.Errorf("find bank account: %w", err)
	}
	if err := s.repo.SoftDeleteBankAccount(ctx, req.ID.Int64(), req.ActorID.Int64()); err != nil {
		return fmt.Errorf("delete bank account: %w", err)
	}
	return nil
}

func (s *Service) GetTaxInfo(ctx context.Context, req financeapi.GetTaxInfoRequest) (financeapi.TaxInfo, error) {
	t, err := s.repo.FindTaxInfo(ctx, req.ActorID.Int64())
	if err != nil {
		return financeapi.TaxInfo{}, fmt.Errorf("find tax info: %w", err)
	}
	return toAPITaxInfo(t), nil
}

// PutTaxInfo files a registration, replacing whatever was on file. Filing again resets
// the verdict on purpose: the details changed, so the previous verification was of
// something else.
func (s *Service) PutTaxInfo(ctx context.Context, req financeapi.PutTaxInfoRequest) (financeapi.TaxInfo, error) {
	t, err := domain.NewTaxInfo(req.ActorID.Int64(), req.TaxCode, req.TaxCodeType, req.LegalName)
	if err != nil {
		return financeapi.TaxInfo{}, err
	}
	if err := s.repo.PutTaxInfo(ctx, t); err != nil {
		return financeapi.TaxInfo{}, fmt.Errorf("put tax info: %w", err)
	}
	stored, err := s.repo.FindTaxInfo(ctx, req.ActorID.Int64())
	if err != nil {
		return financeapi.TaxInfo{}, fmt.Errorf("find tax info: %w", err)
	}
	return toAPITaxInfo(stored), nil
}

// AdminVerifyTaxInfo records the decision. Only a pending registration is decidable:
// re-deciding a settled one would rewrite history rather than add to it.
func (s *Service) AdminVerifyTaxInfo(ctx context.Context, req financeapi.VerifyTaxInfoRequest) (financeapi.TaxInfo, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return financeapi.TaxInfo{}, err
	}
	t, err := s.repo.FindTaxInfo(ctx, req.AccountID.Int64())
	if err != nil {
		return financeapi.TaxInfo{}, fmt.Errorf("find tax info: %w", err)
	}
	if err := t.Verify(req.Verified, req.Source); err != nil {
		return financeapi.TaxInfo{}, err
	}
	if err := s.repo.SaveTaxInfo(ctx, t); err != nil {
		return financeapi.TaxInfo{}, fmt.Errorf("save tax info: %w", err)
	}
	return toAPITaxInfo(t), nil
}

// toAPIBankAccount masks the number: the full value leaves this system only towards
// the bank, so a leaked response or a screenshot cannot carry it.
func toAPIBankAccount(b domain.BankAccount) financeapi.BankAccount {
	return financeapi.BankAccount{
		ID:                  id.Of[id.BankAccount](b.ID),
		BankCode:            b.BankCode,
		AccountNumberMasked: mask(b.AccountNumber),
		AccountHolder:       b.AccountHolder,
		IsDefault:           b.IsDefault,
		CreatedAt:           b.CreatedAt,
	}
}

// mask keeps the last four digits, which is what a person recognises their own account
// by, and hides the rest.
func mask(number string) string {
	const shown = 4
	if len(number) <= shown {
		return "****"
	}
	return "****" + number[len(number)-shown:]
}

func toAPITaxInfo(t domain.TaxInfo) financeapi.TaxInfo {
	return financeapi.TaxInfo{
		TaxCode:            t.TaxCode,
		TaxCodeType:        t.TaxCodeType,
		LegalName:          t.LegalName,
		VerificationStatus: t.VerificationStatus,
		VerifiedAt:         t.VerifiedAt,
		UpdatedAt:          t.UpdatedAt,
	}
}

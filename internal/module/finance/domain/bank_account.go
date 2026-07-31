package domain

import (
	"regexp"
	"strings"
	"time"

	"shopnexus/internal/shared/validation"
)

// bankCodeRe is the shape of a Vietnamese bank's short code — a slug, like every
// other option id in this codebase.
var bankCodeRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// BankAccount is a saved payout destination. Soft-deleted rather than removed,
// because a completed withdrawal names this row from its session data and that is
// the record of where real money went — financial history cannot lose its payee.
type BankAccount struct {
	ID            int64
	AccountID     int64  `validate:"required"`
	BankCode      string `validate:"required,max=20"`
	AccountNumber string `validate:"required,max=50"`
	AccountHolder string `validate:"required,max=100"`
	IsDefault     bool
	CreatedAt     time.Time
	DeletedAt     *time.Time
}

func NewBankAccount(accountID int64, bankCode, number, holder string, isDefault bool) (BankAccount, error) {
	b := BankAccount{
		AccountID:     accountID,
		BankCode:      strings.ToLower(strings.TrimSpace(bankCode)),
		AccountNumber: strings.TrimSpace(number),
		AccountHolder: strings.TrimSpace(holder),
		IsDefault:     isDefault,
	}
	if err := validation.Default().Struct(b); err != nil {
		return BankAccount{}, validation.AsError(err)
	}
	if !bankCodeRe.MatchString(b.BankCode) {
		return BankAccount{}, ErrBankCodeInvalid
	}
	if !digitsOnly(b.AccountNumber) {
		return BankAccount{}, ErrAccountNumberInvalid
	}
	return b, nil
}

// IsLive reports whether the destination may still be paid to.
func (b BankAccount) IsLive() bool { return b.DeletedAt == nil }

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

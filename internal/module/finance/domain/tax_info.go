package domain

import (
	"regexp"
	"strings"
	"time"

	"shopnexus/internal/shared/validation"
)

// Tax code types (kebab-case).
const (
	TaxCodeIndividual = "individual"
	TaxCodeBusiness   = "business"
	TaxCodeHousehold  = "household"
)

// Verification lifecycle, shared with the identity flow's vocabulary.
const (
	VerificationPending  = "pending"
	VerificationVerified = "verified"
	VerificationRejected = "rejected"
)

// taxCodeRe is the Vietnamese MST: ten digits, optionally a three-digit branch.
var taxCodeRe = regexp.MustCompile(`^\d{10}(-\d{3})?$`)

// TaxInfo is one account's tax registration. Keyed by the account, so filing again
// replaces the row rather than stacking — and that reset is exactly the event an
// auditor asks the date of, which is why UpdatedAt is not decoration.
type TaxInfo struct {
	AccountID          int64  `validate:"required"`
	TaxCode            string `validate:"required"`
	TaxCodeType        string `validate:"required,oneof=individual business household"`
	LegalName          string `validate:"required,max=200"`
	VerificationStatus string `validate:"required,oneof=pending verified rejected"`
	VerifiedAt         *time.Time
	VerificationSource *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// NewTaxInfo files a registration. It always starts unverified: a seller declaring
// their own tax code verified is the whole thing this flow exists to prevent.
func NewTaxInfo(accountID int64, code, codeType, legalName string) (TaxInfo, error) {
	t := TaxInfo{
		AccountID:          accountID,
		TaxCode:            strings.TrimSpace(code),
		TaxCodeType:        codeType,
		LegalName:          strings.TrimSpace(legalName),
		VerificationStatus: VerificationPending,
	}
	if err := validation.Default().Struct(t); err != nil {
		return TaxInfo{}, validation.AsError(err)
	}
	if !taxCodeRe.MatchString(t.TaxCode) {
		return TaxInfo{}, ErrTaxCodeInvalid
	}
	return t, nil
}

// Verify records a decision. Only a pending registration is decidable: re-deciding
// a settled one would rewrite history rather than add to it, and a seller who wants
// a new verdict files again.
func (t *TaxInfo) Verify(verified bool, source string) error {
	if t.VerificationStatus != VerificationPending {
		return ErrTaxInfoSettled
	}
	if verified {
		t.VerificationStatus = VerificationVerified
		t.VerifiedAt = new(time.Now())
	} else {
		t.VerificationStatus = VerificationRejected
	}
	if source != "" {
		t.VerificationSource = &source
	}
	t.UpdatedAt = time.Now()
	return nil
}

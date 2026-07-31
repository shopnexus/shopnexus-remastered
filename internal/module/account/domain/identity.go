package domain

import "time"

// DocType is the government ID a vendor checked.
type DocType string

const (
	DocTypeNationalID    DocType = "national-id"
	DocTypePassport      DocType = "passport"
	DocTypeDriverLicense DocType = "driver-license"
)

type IdentityStatus string

const (
	IdentityPending  IdentityStatus = "pending"
	IdentityVerified IdentityStatus = "verified"
	IdentityRejected IdentityStatus = "rejected"
)

// IdentityDocument is one verification outcome — no document number and no scan,
// only the vendor, its case reference and the verdict.
type IdentityDocument struct {
	ID        int64
	AccountID int64
	DocType   DocType
	Provider  string
	// ProviderRef is the vendor's case id, for re-reading the verdict later.
	ProviderRef     string
	Status          IdentityStatus
	RejectionReason *string
	VerifiedAt      *time.Time
	// ExpiresAt is when the document itself runs out. A payout gate reads this as
	// well as the status, which is why a passport's expiry is not optional detail.
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// IdentityDocumentSnapshot is the row as the audit log keeps it. The document number and
// the scans are not here and never were — a leak of the trail must not impersonate anyone.
type IdentityDocumentSnapshot struct {
	ID              int64          `json:"id"`
	AccountID       int64          `json:"account_id"`
	DocType         DocType        `json:"doc_type"`
	Provider        string         `json:"provider"`
	Status          IdentityStatus `json:"status"`
	RejectionReason *string        `json:"rejection_reason"`
	VerifiedAt      *time.Time     `json:"verified_at"`
	ExpiresAt       *time.Time     `json:"expires_at"`
}

func (d IdentityDocument) Snapshot() IdentityDocumentSnapshot {
	return IdentityDocumentSnapshot{
		ID:              d.ID,
		AccountID:       d.AccountID,
		DocType:         d.DocType,
		Provider:        d.Provider,
		Status:          d.Status,
		RejectionReason: d.RejectionReason,
		VerifiedAt:      d.VerifiedAt,
		ExpiresAt:       d.ExpiresAt,
	}
}

// expiringDocTypes are the ones that carry an expiry date, so a verdict of
// 'verified' has to come with one.
var expiringDocTypes = map[DocType]bool{
	DocTypePassport:      true,
	DocTypeDriverLicense: true,
	// A national id is issued for life in some markets, so it is not on the list.
}

// NewIdentityDocument opens a case. It starts pending whatever the vendor is about to
// say, so a verdict always goes through Verify or Reject and the rules those enforce —
// an expiry for a document type that has one, a reason for a refusal — hold for an
// automated check exactly as they do for a moderator's.
func NewIdentityDocument(accountID int64, docType DocType, provider, providerRef string) (IdentityDocument, error) {
	if provider == "" || providerRef == "" {
		return IdentityDocument{}, ErrIdentityVendorIncomplete
	}
	return IdentityDocument{
		AccountID:   accountID,
		DocType:     docType,
		Provider:    provider,
		ProviderRef: providerRef,
		Status:      IdentityPending,
	}, nil
}

// IsLive reports whether the document still proves anything: verified, and not past
// its own expiry. This is what the payout gate asks.
func (d IdentityDocument) IsLive(now time.Time) bool {
	if d.Status != IdentityVerified {
		return false
	}
	return d.ExpiresAt == nil || d.ExpiresAt.After(now)
}

// Verify records a moderator's approval. The expiry is required for a document type
// that has one, because a status alone would let an expired passport pass the gate
// forever.
func (d *IdentityDocument) Verify(now time.Time, expiresAt *time.Time) error {
	if d.Status != IdentityPending {
		return ErrIdentityAlreadyDecided
	}
	if expiresAt == nil && expiringDocTypes[d.DocType] {
		return ErrIdentityExpiryRequired
	}
	d.Status = IdentityVerified
	d.VerifiedAt = &now
	d.ExpiresAt = expiresAt
	d.RejectionReason = nil
	return nil
}

// Reject records a refusal. The reason is required: it is what the account is shown
// and what a second reviewer reads.
func (d *IdentityDocument) Reject(reason string) error {
	if d.Status != IdentityPending {
		return ErrIdentityAlreadyDecided
	}
	if reason == "" {
		return ErrRejectionReasonRequired
	}
	d.Status = IdentityRejected
	d.RejectionReason = &reason
	d.VerifiedAt = nil
	return nil
}

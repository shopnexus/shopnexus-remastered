package domain_test

import (
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
)

func pending(docType domain.DocType) domain.IdentityDocument {
	return domain.IdentityDocument{DocType: docType, Provider: "mock", Status: domain.IdentityPending}
}

// A passport runs out, so approving one without an expiry would leave the payout gate
// reading a status that stays true forever.
func TestVerify_ExpiringTypeRequiresExpiry(t *testing.T) {
	d := pending(domain.DocTypePassport)
	if got := status(t, d.Verify(time.Now(), nil)); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
	if d.Status != domain.IdentityPending {
		t.Fatalf("status = %q, want the document untouched", d.Status)
	}
}

// A national id is issued for life in some markets, so it is verifiable without one.
func TestVerify_NonExpiringTypeNeedsNoExpiry(t *testing.T) {
	d := pending(domain.DocTypeNationalID)
	if err := d.Verify(time.Now(), nil); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if d.Status != domain.IdentityVerified || d.VerifiedAt == nil {
		t.Fatalf("document = %+v, want verified with a timestamp", d)
	}
}

func TestReject_RequiresAReason(t *testing.T) {
	d := pending(domain.DocTypeNationalID)
	if got := status(t, d.Reject("")); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
	if err := d.Reject("blurry scan"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if d.Status != domain.IdentityRejected || d.RejectionReason != "blurry scan" {
		t.Fatalf("document = %+v", d)
	}
}

// Only a pending document takes a verdict, so a second moderator cannot overwrite the
// first one's decision.
func TestVerdict_OnlyOnce(t *testing.T) {
	d := pending(domain.DocTypeNationalID)
	if err := d.Verify(time.Now(), nil); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := status(t, d.Reject("changed my mind")); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// IsLive is the payout gate's question, and the status alone does not answer it.
func TestIsLive(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	for _, tc := range []struct {
		name string
		doc  domain.IdentityDocument
		want bool
	}{
		{name: "pending", doc: pending(domain.DocTypePassport), want: false},
		{name: "verified, no expiry", doc: domain.IdentityDocument{Status: domain.IdentityVerified}, want: true},
		{name: "verified, still valid", doc: domain.IdentityDocument{Status: domain.IdentityVerified, ExpiresAt: &future}, want: true},
		{name: "verified, expired", doc: domain.IdentityDocument{Status: domain.IdentityVerified, ExpiresAt: &past}, want: false},
		{name: "rejected", doc: domain.IdentityDocument{Status: domain.IdentityRejected}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.doc.IsLive(now); got != tc.want {
				t.Fatalf("IsLive() = %v, want %v", got, tc.want)
			}
		})
	}
}

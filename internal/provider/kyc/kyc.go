// Package kyc is the seam to the identity-verification vendor.
//
// The vendor does the checking and we keep only its verdict: a case reference, a status,
// an expiry. No document number and no scan crosses this interface, which is what makes
// a leak of the account schema unable to impersonate anyone.
//
// Two vendor shapes fit behind Check. One reads the scans itself and answers straight
// away (FPT.AI, VNPT) — those return a decided Result. The other hands the check to the
// user in its own web flow (Sumsub, Onfido) — those return a pending Result plus the
// SessionURL to send them to. The caller stores whichever came back and does not care
// which kind it is talking to.
package kyc

import (
	"context"
	"time"
)

// Status is the verdict. It mirrors the account module's identity_status, without
// importing it: a provider must not depend on the module that consumes it.
type Status string

const (
	// StatusPending means nobody has decided yet — the vendor is asynchronous, or it
	// does not check this document type and a moderator will.
	StatusPending  Status = "pending"
	StatusVerified Status = "verified"
	StatusRejected Status = "rejected"
)

// Image points at bytes the caller has already stored. A URL rather than the bytes
// themselves so a 5 MB photo is streamed by whoever needs it instead of being buffered
// through the request that started the check.
type Image struct {
	// URL is a short-lived link to the object, as issued by the storage provider.
	URL string
	// Mime is what the storage layer recorded, used for the upload part's content type.
	Mime string
}

func (i Image) Present() bool { return i.URL != "" }

// CheckParams is one verification. AccountRef is the *opaque* account id, so the raw
// database key is never handed to a third party.
type CheckParams struct {
	AccountRef string
	DocType    string
	Locale     string
	// Front is the data page or the front of the card, Back the reverse where the
	// document has one, Selfie the live photo matched against the document portrait.
	Front  Image
	Back   Image
	Selfie Image
}

// Result is what the vendor said.
type Result struct {
	// Provider is the vendor name, kebab-case, stored on the document row.
	Provider string
	// Ref identifies the check with the vendor, for re-reading the verdict later.
	Ref    string
	Status Status
	// RejectionReason is set only when Status is rejected, and is shown to the account.
	RejectionReason string
	// ExpiresAt is when the *document* runs out, as read off it. A payout gate reads it
	// as well as the status.
	ExpiresAt *time.Time
	// SessionURL and SessionExpiresAt are set only by a vendor that finishes the check
	// in its own web flow.
	SessionURL       string
	SessionExpiresAt *time.Time
}

type Client interface {
	// Check runs the vendor's verification. It applies its own per-operation timeouts,
	// since both the scan download and the vendor call leave the process.
	Check(ctx context.Context, p CheckParams) (Result, error)
}

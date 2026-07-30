// Package mock is a dev-only KYC vendor: it accepts any scan, opens a case nobody
// checks, and leaves the verdict pending so the flow can be finished by a moderator
// through POST /admin/identity-documents/{id}/verdict — which is also how a real
// vendor's answer is overridden.
//
// It returns a session URL that goes nowhere, so the hosted-flow shape of the seam is
// exercised in dev even though the vendor chosen for production reads the scans itself.
package mock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"shopnexus/internal/provider/kyc"
)

// sessionTTL is how long the fake vendor session stays open — short, like a real one, so
// a client that ignores the expiry is caught in dev.
const sessionTTL = 15 * time.Minute

var _ kyc.Client = (*Client)(nil)

type Client struct{}

func NewClient() *Client { return &Client{} }

func (*Client) Check(_ context.Context, p kyc.CheckParams) (kyc.Result, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return kyc.Result{}, fmt.Errorf("read random bytes: %w", err)
	}
	ref := hex.EncodeToString(b[:])
	expires := time.Now().Add(sessionTTL)
	return kyc.Result{
		Provider:         "mock",
		Ref:              ref,
		Status:           kyc.StatusPending,
		SessionURL:       "https://kyc.mock.invalid/sessions/" + ref + "?doc_type=" + p.DocType,
		SessionExpiresAt: &expires,
	}, nil
}

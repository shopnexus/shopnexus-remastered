// Package mock is a dev-only oauth verifier: the credential *is* the assertion,
// so a developer exercises the federated sign-in flow without registering an app
// with Google or Apple.
//
// Format: "<subject>" or "<subject>:<email>". A subject with no email stands in
// for Apple's "hide my email", which is the case worth testing — the account has
// to end up with a generated username instead.
package mock

import (
	"context"
	"strings"

	"shopnexus/internal/provider/oauth"
)

// known is the set of provider names this mock answers for, so an unsupported
// provider still fails the way a real deployment would.
var known = map[string]bool{"google": true, "facebook": true, "apple": true, "zalo": true}

var _ oauth.Verifier = (*Verifier)(nil)

type Verifier struct{}

func NewVerifier() *Verifier { return &Verifier{} }

func (*Verifier) Verify(_ context.Context, provider, credential string) (oauth.Identity, error) {
	if !known[provider] {
		return oauth.Identity{}, oauth.ErrUnknownProvider.Fmt(provider)
	}
	subject, email, _ := strings.Cut(credential, ":")
	if subject == "" {
		return oauth.Identity{}, oauth.ErrRejected
	}
	return oauth.Identity{
		Provider: provider,
		Subject:  subject,
		Email:    email,
		// A mock that returned unverified emails could never exercise the merge path,
		// which is the interesting half of federated sign-in.
		EmailVerified: email != "",
		Name:          subject,
	}, nil
}

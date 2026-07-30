// Package oauth verifies a federated sign-in credential and reports who the
// provider says the caller is.
//
// One interface covers every provider rather than one per vendor: the provider
// name arrives in the request body, so the choice is data, and a real
// implementation dispatches on it internally (Google and Apple both hand back an
// OIDC id token; a `github.com/coreos/go-oidc` verifier per issuer is the obvious
// shape). Nothing above this seam knows which.
package oauth

import (
	"context"
	"net/http"

	"shopnexus/internal/shared/errx"
)

// ErrRejected is a credential the provider would not vouch for — expired,
// tampered with, or issued to another audience. 401 rather than 400: the request
// was well formed and the *credential* is what failed.
var ErrRejected = errx.NewError(http.StatusUnauthorized, "oauth_rejected", "the provider rejected this credential")

// ErrUnknownProvider is a provider nobody is configured for. 422: the body is
// valid and there is simply no such way to sign in here.
var ErrUnknownProvider = errx.NewErrorf(http.StatusUnprocessableEntity, "oauth_unknown_provider", "unsupported oauth provider: %s")

// Identity is what the provider asserts. Subject is its stable subject id — never
// the email, which a user can change at the provider and which is not unique
// across providers.
type Identity struct {
	Provider string
	Subject  string
	Email    string
	// EmailVerified is why an asserted email may or may not merge into an existing
	// local account: only a provider-verified address is trustworthy enough to take
	// over an account that already exists.
	EmailVerified bool
	// Name is a display name, used when the provider's account becomes a new local
	// one and there is no profile yet.
	Name string
}

type Verifier interface {
	// Verify exchanges or validates the provider's credential (an authorization
	// code or an id token). It applies its own per-operation timeout, since the
	// call leaves the process.
	Verify(ctx context.Context, provider, credential string) (Identity, error)
}

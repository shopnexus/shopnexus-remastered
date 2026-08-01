// Package oidc verifies a federated sign-in credential as an OpenID Connect id token.
//
// The credential a client posts is the id token the provider already issued to it, and
// verifying one is a local operation once the issuer's signing keys are known: check the
// signature, the issuer, the audience and the expiry. That is why this package holds no
// client secret and performs no code exchange — the secret belongs to whoever redeemed
// the authorization code, and asking the API to redeem it again would mean shipping a
// second copy of it.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"

	"shopnexus/internal/provider/oauth"
)

// Name is the OAUTH_VERIFIER value that selects this verifier.
const Name = "oidc"

// Issuer describes one provider this deployment accepts.
type Issuer struct {
	// URL is the OIDC issuer, e.g. "https://accounts.google.com". Discovery hangs off
	// it, and the id token's "iss" must equal it.
	URL string
	// Audiences are the client ids tokens may be issued to. A provider usually has
	// more than one — an iOS bundle id, an Android client, a web client — and every
	// one of them is a legitimate audience for the same account.
	Audiences []string
}

type Config struct {
	// Issuers is keyed by the provider name the API speaks ("google", "apple"), which
	// is what arrives in the request body. A provider that is not here is refused
	// rather than guessed at.
	Issuers map[string]Issuer
	// Timeout bounds one verification, including the discovery and JWKS fetches it may
	// trigger. Required: the first call after a key rotation is the one that goes to
	// the network, and it happens on a sign-in request.
	Timeout time.Duration
	// HTTPClient is optional and must not carry a Timeout of its own — the budget above
	// is applied to the request context, so an instrumented transport can be layered in.
	HTTPClient *http.Client
}

var _ oauth.Verifier = (*Verifier)(nil)

type Verifier struct {
	issuers map[string]Issuer
	timeout time.Duration
	http    *http.Client

	// Verifiers are built once per issuer and reused, which is not an optimisation: a
	// verifier owns the remote key set, and building one per request would fetch the
	// provider's JWKS on every single sign-in — an issuer with three audiences would
	// fetch it three times per attempt.
	//
	// Lazily, not at startup: a marketplace should come up and serve its catalogue even
	// while Apple's discovery endpoint is down.
	mu    sync.Mutex
	built map[string][]*coreoidc.IDTokenVerifier
}

func NewVerifier(cfg Config) (*Verifier, error) {
	if len(cfg.Issuers) == 0 {
		return nil, errors.New("oidc config: at least one issuer is required")
	}
	for name, issuer := range cfg.Issuers {
		if issuer.URL == "" {
			return nil, fmt.Errorf("oidc config: issuer %q has no url", name)
		}
		if len(issuer.Audiences) == 0 {
			return nil, fmt.Errorf("oidc config: issuer %q has no audience", name)
		}
	}
	if cfg.Timeout <= 0 {
		return nil, errors.New("oidc config: timeout is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Verifier{
		issuers: cfg.Issuers,
		timeout: cfg.Timeout,
		http:    httpClient,
		built:   map[string][]*coreoidc.IDTokenVerifier{},
	}, nil
}

// claims is the subset of the id token this API uses. Apple sends email_verified as the
// *string* "true", which is why it is not a bool here — a provider that stretches the
// spec must not turn into a 500.
type claims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
}

func (v *Verifier) Verify(ctx context.Context, provider, credential string) (oauth.Identity, error) {
	issuer, ok := v.issuers[provider]
	if !ok {
		return oauth.Identity{}, oauth.ErrUnknownProvider.Fmt(provider)
	}

	verifiers, err := v.verifiers(provider, issuer)
	if err != nil {
		// Discovery failing is our problem, not a bad credential: it must not come back
		// as "the provider rejected you", or a support desk chases the wrong thing.
		return oauth.Identity{}, fmt.Errorf("oidc discovery for %q: %w", provider, err)
	}

	// The request's own deadline bounds the verification, including a JWKS refetch if the
	// token is signed with a key this process has not seen yet.
	ctx, cancel := context.WithTimeout(coreoidc.ClientContext(ctx, v.http), v.timeout)
	defer cancel()

	// One verifier per audience: go-oidc checks a single client id at a time, and a token
	// issued to the Android client is as valid as one issued to the web client.
	var lastErr error
	for _, verifier := range verifiers {
		token, err := verifier.Verify(ctx, credential)
		if err != nil {
			lastErr = err
			continue
		}
		return identityOf(provider, token)
	}
	// Every audience refused it: expired, tampered with, or issued to somebody else.
	// All three are the same answer to the caller.
	return oauth.Identity{}, fmt.Errorf("%w: %v", oauth.ErrRejected, lastErr)
}

// verifiers returns the cached verifiers for an issuer, discovering it on first use.
//
// They are built on a background context rather than the caller's: the key set they own
// outlives this request, and a cancelled context stored inside it would break every later
// sign-in. The discovery call itself is still bounded by the configured timeout.
func (v *Verifier) verifiers(name string, issuer Issuer) ([]*coreoidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if built, ok := v.built[name]; ok {
		return built, nil
	}

	ctx, cancel := context.WithTimeout(coreoidc.ClientContext(context.Background(), v.http), v.timeout)
	defer cancel()
	p, err := coreoidc.NewProvider(ctx, issuer.URL)
	if err != nil {
		return nil, err
	}

	built := make([]*coreoidc.IDTokenVerifier, 0, len(issuer.Audiences))
	for _, audience := range issuer.Audiences {
		built = append(built, p.Verifier(&coreoidc.Config{ClientID: audience}))
	}
	v.built[name] = built
	return built, nil
}

func identityOf(provider string, token *coreoidc.IDToken) (oauth.Identity, error) {
	var c claims
	if err := token.Claims(&c); err != nil {
		return oauth.Identity{}, fmt.Errorf("read id token claims: %w", err)
	}
	if c.Subject == "" {
		// Without a stable subject there is nothing to link an account to, and falling
		// back to the email would let a changed address take over an account.
		return oauth.Identity{}, fmt.Errorf("%w: id token has no subject", oauth.ErrRejected)
	}
	name := c.Name
	if name == "" {
		name = c.GivenName
	}
	return oauth.Identity{
		Provider:      provider,
		Subject:       c.Subject,
		Email:         c.Email,
		EmailVerified: truthy(c.EmailVerified),
		Name:          name,
	}, nil
}

// truthy reads the claim whichever way the provider spelled it. Absent means false: an
// unverified address may not merge into an existing account, so the safe reading of "I
// cannot tell" is "not verified".
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	default:
		return false
	}
}

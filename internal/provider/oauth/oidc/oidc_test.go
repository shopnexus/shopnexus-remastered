package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"shopnexus/internal/provider/oauth"
	oidcverify "shopnexus/internal/provider/oauth/oidc"
)

// A whole fake issuer: discovery document, JWKS, and a signing key. Verifying an id token
// is the security boundary of federated sign-in, so it is tested against real RS256
// signatures rather than a stub that says yes.
type issuer struct {
	key  *rsa.PrivateKey
	url  string
	jwks int // how many times the key set was fetched
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	iss := &issuer{key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.url,
			"authorization_endpoint":                iss.url + "/auth",
			"token_endpoint":                        iss.url + "/token",
			"jwks_uri":                              iss.url + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		iss.jwks++
		writeJSON(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA",
			"kid": "test-key",
			"alg": "RS256",
			"use": "sig",
			"n":   b64(key.N.Bytes()),
			"e":   b64(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	iss.url = srv.URL
	return iss
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, v)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// sign mints an id token the way a provider would.
func (i *issuer) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = i.url
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	claims["iat"] = time.Now().Unix()

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(i.key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func newVerifier(t *testing.T, iss *issuer, audiences ...string) oauth.Verifier {
	t.Helper()
	v, err := oidcverify.NewVerifier(oidcverify.Config{
		Issuers: map[string]oidcverify.Issuer{
			"google": {URL: iss.url, Audiences: audiences},
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestVerify_ValidTokenMapsTheClaims(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	token := iss.sign(t, jwt.MapClaims{
		"aud":            "client-1",
		"sub":            "provider-subject-1",
		"email":          "alice@example.com",
		"email_verified": true,
		"name":           "Alice",
	})
	got, err := v.Verify(context.Background(), "google", token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := oauth.Identity{
		Provider: "google", Subject: "provider-subject-1",
		Email: "alice@example.com", EmailVerified: true, Name: "Alice",
	}
	if got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
}

// Apple sends email_verified as the string "true". A provider stretching the spec must not
// turn into a 500, and must not silently read as unverified either.
func TestVerify_StringEmailVerified(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	got, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{
		"aud": "client-1", "sub": "s", "email": "a@b.com", "email_verified": "true",
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !got.EmailVerified {
		t.Fatal("email_verified sent as \"true\" was read as false")
	}
}

// An absent claim reads as unverified: an unverified address may not merge into an existing
// account, so "I cannot tell" has to mean no.
func TestVerify_MissingEmailVerifiedIsFalse(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	got, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{
		"aud": "client-1", "sub": "s", "email": "a@b.com",
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.EmailVerified {
		t.Fatal("a missing email_verified claim was read as verified")
	}
}

// A provider usually has several client ids — an iOS bundle, an Android client, a web
// client — and a token issued to any of them belongs to the same account.
func TestVerify_AnyConfiguredAudienceIsAccepted(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "web-client", "ios-client")

	for _, aud := range []string{"web-client", "ios-client"} {
		if _, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{"aud": aud, "sub": "s"})); err != nil {
			t.Errorf("Verify with aud %q: %v", aud, err)
		}
	}
}

// A token issued to somebody else's app is not a sign-in here: accepting it would let any
// developer with a Google client mint sessions for our users.
func TestVerify_ForeignAudienceRejected(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	_, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{"aud": "someone-elses-app", "sub": "s"}))
	if !errors.Is(err, oauth.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

func TestVerify_ExpiredTokenRejected(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	_, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{
		"aud": "client-1", "sub": "s", "exp": time.Now().Add(-time.Hour).Unix(),
	}))
	if !errors.Is(err, oauth.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

// Signed by a key the issuer does not publish: the whole point of fetching the JWKS.
func TestVerify_ForeignSignatureRejected(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")
	attacker := newIssuer(t)

	// Same claims, someone else's key.
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": iss.url, "aud": "client-1", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "test-key"
	forged, err := tok.SignedString(attacker.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := v.Verify(context.Background(), "google", forged); !errors.Is(err, oauth.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

// A token with no subject cannot be linked to an account, and falling back to the email
// would let a changed address take one over.
func TestVerify_NoSubjectRejected(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	// go-oidc requires a subject itself, so this is a belt-and-braces check on the same
	// rule from the other side.
	_, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{"aud": "client-1"}))
	if err == nil {
		t.Fatal("expected an error for a token with no subject")
	}
}

// A provider nobody configured is refused rather than guessed at, and it is a 422: the
// request is well formed and there is simply no such way to sign in here.
func TestVerify_UnknownProvider(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	_, err := v.Verify(context.Background(), "facebook", "whatever")
	if err == nil {
		t.Fatal("expected an error for an unconfigured provider")
	}
	if !strings.Contains(err.Error(), "facebook") {
		t.Errorf("error should name the provider, got %v", err)
	}
}

// Discovery is one round trip whose result does not change, so it happens once — and
// lazily, so a provider being down does not stop the process from starting.
func TestVerify_DiscoveryHappensOncePerIssuer(t *testing.T) {
	iss := newIssuer(t)
	v := newVerifier(t, iss, "client-1")

	for i := 0; i < 3; i++ {
		if _, err := v.Verify(context.Background(), "google", iss.sign(t, jwt.MapClaims{"aud": "client-1", "sub": "s"})); err != nil {
			t.Fatalf("Verify #%d: %v", i+1, err)
		}
	}
	// The key set may be re-fetched on rotation, but not once per sign-in.
	if iss.jwks > 1 {
		t.Errorf("jwks was fetched %d times for 3 verifications", iss.jwks)
	}
}

func TestNewVerifier_RequiredFields(t *testing.T) {
	good := map[string]oidcverify.Issuer{"google": {URL: "https://accounts.google.com", Audiences: []string{"c"}}}
	for name, cfg := range map[string]oidcverify.Config{
		"no issuer":   {Timeout: time.Second},
		"no url":      {Issuers: map[string]oidcverify.Issuer{"google": {Audiences: []string{"c"}}}, Timeout: time.Second},
		"no audience": {Issuers: map[string]oidcverify.Issuer{"google": {URL: "https://accounts.google.com"}}, Timeout: time.Second},
		"no timeout":  {Issuers: good},
	} {
		if _, err := oidcverify.NewVerifier(cfg); err == nil {
			t.Errorf("expected an error: %s", name)
		}
	}
}

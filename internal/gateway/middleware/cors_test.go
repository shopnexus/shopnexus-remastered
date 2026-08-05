package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"shopnexus/internal/gateway/middleware"
)

const site = "https://shopnexus.github.io"

// corsHandler wraps a route that records whether it was reached: a preflight that gets routed is
// an unauthenticated OPTIONS on every path, so "did not reach the route" is part of the contract.
func corsHandler(origins ...string) (http.Handler, *bool) {
	reached := false
	h := middleware.CORS(middleware.NormalizeOrigins(origins))(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))
	return h, &reached
}

func TestCORS_PreflightFromAnAllowedOriginIsAnsweredAndNotRouted(t *testing.T) {
	h, reached := corsHandler(site)

	r := httptest.NewRequest(http.MethodOptions, "/api/v1/listings", nil)
	r.Header.Set("Origin", site)
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	r.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if *reached {
		t.Error("the preflight reached the route, so every path answers an untokened OPTIONS")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != site {
		t.Errorf("allow-origin = %q, want the origin echoed back", got)
	}
	// Echoed, not listed: the header the client actually set has to be the one allowed, or the
	// bearer token never leaves the browser.
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "authorization,content-type" {
		t.Errorf("allow-headers = %q, want what the browser asked for", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("allow-methods is empty, so the browser has no verb to send")
	}
	// A cache keyed without this would hand one origin's allow header to another.
	if got := rec.Header().Values("Vary"); len(got) == 0 {
		t.Error("no Vary, so a proxy may reuse this reply for a different origin")
	}
}

// A refusal is a reply with no allow header, not a 403: the browser is asking whether it may
// call, and it is the missing header that answers no.
func TestCORS_PreflightFromAnUnknownOriginCarriesNoAllowHeader(t *testing.T) {
	h, reached := corsHandler(site)

	r := httptest.NewRequest(http.MethodOptions, "/api/v1/listings", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if *reached {
		t.Error("the preflight reached the route")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q, want none for an origin nobody allowed", got)
	}
}

func TestCORS_ARealRequestIsRoutedAndStamped(t *testing.T) {
	h, reached := corsHandler(site)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/listings", nil)
	r.Header.Set("Origin", site)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if !*reached {
		t.Fatal("the request never reached the route")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != site {
		t.Errorf("allow-origin = %q, want the origin echoed back", got)
	}
	// Without this the page cannot read the request id it would put in a bug report.
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Error("expose-headers is empty, so a script sees only the safelisted headers")
	}
}

func TestCORS_StarAllowsAnyOrigin(t *testing.T) {
	h, _ := corsHandler("*")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/listings", nil)
	r.Header.Set("Origin", "https://anywhere.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	// The origin, not a literal `*`: echoing it keeps the reply valid if credentials are ever
	// added, and a browser treats the two the same when they are not.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anywhere.example" {
		t.Errorf("allow-origin = %q, want the caller's origin", got)
	}
}

// A same-origin call has no Origin header at all, and must not be given one.
func TestCORS_NoOriginHeaderIsLeftAlone(t *testing.T) {
	h, reached := corsHandler(site)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/listings", nil))

	if !*reached {
		t.Fatal("the request never reached the route")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q, want none when no origin was sent", got)
	}
}

// An operator pastes a site URL, and a browser sends scheme://host. Matching those literally is
// an allowlist that silently matches nothing.
func TestNormalizeOrigins_TrimsWhatAnOperatorPastes(t *testing.T) {
	got := middleware.NormalizeOrigins([]string{
		"https://shopnexus.github.io/",
		" http://localhost:3000 ",
		"https://shopnexus.github.io/shopnexus-web/",
		"*",
	})
	want := []string{site, "http://localhost:3000", site, "*"}
	if len(got) != len(want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("normalized[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

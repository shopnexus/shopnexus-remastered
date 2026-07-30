package gateway_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	openapi "shopnexus/api"

	"gopkg.in/yaml.v3"
)

// TestOpenAPIContract_RefsResolve guards the merged spec's internal
// consistency: every $ref must point at a component that exists.
//
// This is the half of contract drift that is actually checkable. cmd/specgen
// merges each module fragment's components.schemas into one flat namespace, so a
// fragment can reference a schema another module owns (order's CheckoutResult
// refs finance's PaymentSession). Renaming that schema in its owning fragment
// would otherwise produce a spec that serves and validates as YAML while
// pointing at nothing.
func TestOpenAPIContract_RefsResolve(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(openapi.SpecYAML, &doc); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}

	refs := map[string][]string{} // ref -> where it appears
	collectRefs(doc, "", refs)
	if len(refs) == 0 {
		t.Fatal("spec contains no $ref at all, which cannot be right")
	}

	for ref, sites := range refs {
		target, ok := strings.CutPrefix(ref, "#/")
		if !ok {
			t.Errorf("$ref %q at %s is not a local reference", ref, sites[0])
			continue
		}
		if !resolve(doc, strings.Split(target, "/")) {
			t.Errorf("$ref %q does not resolve (referenced from %s)", ref, strings.Join(sites, ", "))
		}
	}
}

// collectRefs walks the document and records every $ref together with the path
// it was found at, so a failure names the operation that broke.
func collectRefs(node any, at string, out map[string][]string) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if k == "$ref" {
				if s, ok := v.(string); ok {
					out[s] = append(out[s], at)
					continue
				}
			}
			collectRefs(v, at+"/"+k, out)
		}
	case []any:
		for i, v := range n {
			collectRefs(v, fmt.Sprintf("%s/%d", at, i), out)
		}
	}
}

func resolve(doc map[string]any, parts []string) bool {
	cur := any(doc)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[p]
		if !ok {
			return false
		}
	}
	return true
}

// TestOpenAPIContract_NoStrayKeys catches a YAML accident the parser accepts.
//
// In a flow mapping — `{ type: integer, description: Bytes, as read from storage }`
// — a comma inside an unquoted description ends the entry, so the remainder
// becomes a second key with a null value. The document still parses and still
// serves; the schema just quietly grows a nonsense field. A backtick in the same
// position happens to be a hard parse error, which is the only reason the first
// one was noticed.
//
// Every key this project writes is snake_case or a path, so a key containing a
// space is always this bug.
func TestOpenAPIContract_NoStrayKeys(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(openapi.SpecYAML, &doc); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}
	var walk func(node any, at string)
	walk = func(node any, at string) {
		switch n := node.(type) {
		case map[string]any:
			for k, v := range n {
				if strings.Contains(k, " ") {
					t.Errorf("stray key %q at %s — an unquoted comma inside a flow mapping split a description", k, at)
				}
				walk(v, at+"/"+k)
			}
		case []any:
			for i, v := range n {
				walk(v, fmt.Sprintf("%s/%d", at, i))
			}
		}
	}
	walk(doc, "")
}

// TestOpenAPIContract_AllPathsRouted requires every documented operation to be
// registered on the real router.
//
// A route may answer 501 — the gateway is scaffolded and most handlers are not
// written yet — but it has to exist, so adding a path to a fragment without
// wiring it fails here. 405 counts as missing too: ServeMux answers 405 when the
// path is registered for a different method, so checking 404 alone would report a
// documented GET as served by a POST-only route.
//
// The opposite direction — a live route with no contract — is the more dangerous
// one and is not checked, because http.ServeMux does not expose its registered
// patterns. Registering routes by hand in router.go is what keeps this test
// meaningful: a router built from the spec at startup would pass by construction.
func TestOpenAPIContract_AllPathsRouted(t *testing.T) {
	var doc struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openapi.SpecYAML, &doc); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("openapi spec declares no paths")
	}

	// Requests are built the way a client would: base + path. If the two disagree,
	// every operation 404s while both halves look right in isolation.
	if len(doc.Servers) == 0 {
		t.Fatal("openapi spec declares no servers, so clients have no base path to call")
	}
	base := doc.Servers[0].URL
	if base != openapi.BasePath {
		t.Fatalf("spec servers[0].url is %q but the router mounts at api.BasePath %q — a client following the spec would 404 on every operation", base, openapi.BasePath)
	}

	r, tm, sessions := newRouter()
	// A real session, because the auth middleware checks the token *and* the session it
	// names; a hand-built token would stop at 401 and every authenticated route would look
	// routed for the wrong reason.
	tok := bearer(t, tm, sessions, 1)
	paramRe := regexp.MustCompile(`\{[^}]+\}`)

	var unrouted []string
	total := 0
	for path, ops := range doc.Paths {
		reqPath := base + paramRe.ReplaceAllString(path, "x")
		for method := range ops {
			m := strings.ToUpper(method)
			switch m {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue // skip non-operation keys (parameters, summary, ...)
			}
			total++
			req := httptest.NewRequest(m, reqPath, strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			// 405 counts as unrouted too: ServeMux answers it when the path is
			// registered but not for this method, so checking 404 alone would
			// report a documented GET as served by a POST-only route.
			if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
				unrouted = append(unrouted, m+" "+path)
			}
		}
	}

	if len(unrouted) > 0 {
		sort.Strings(unrouted)
		t.Errorf("%d/%d documented operations are not registered on the router:\n  %s",
			len(unrouted), total, strings.Join(unrouted, "\n  "))
	}
}

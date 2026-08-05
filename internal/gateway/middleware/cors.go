package middleware

import (
	"net/http"
	"slices"
	"strings"
)

// AllowedOrigins is where a browser may call this API from. Its own type, not a bare []string:
// the fx graph is keyed by type, and the WebSocket's origin list is also a list of strings.
type AllowedOrigins []string

// corsMaxAge is how long a browser may skip re-asking. Ten minutes rather than a day: the
// allowlist changes with a deploy, and a stale preflight is a client that cannot call the new
// route until the cache expires.
const corsMaxAge = "600"

// corsMethods is every verb this API routes. Written out rather than derived from the mux: the
// browser asks about one method and the answer is the same on every path, so a per-route list
// would only differ in ways a client cannot act on.
const corsMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

// corsExposedHeaders are the response headers a script may read. Without this a browser hands the
// page only the six CORS-safelisted ones, so the request id it would put in a bug report is
// invisible to it.
const corsExposedHeaders = "X-Request-Id, X-Object-Size"

// CORS answers the browser's preflight and stamps the allow headers onto a cross-origin reply.
//
// The allowlist holds full origins (`https://shopnexus.github.io`), matched exactly, because that
// is the form `Access-Control-Allow-Origin` has to echo — unlike WS_ALLOWED_ORIGINS, which is a
// host pattern because that is what coder/websocket compares. A lone `*` allows any origin, and it
// is safe *here and only here*: this API authenticates with a bearer token and sets no cookie, so
// `Access-Control-Allow-Credentials` is never sent and a hostile page has nothing to ride along
// on. Introducing a cookie would make `*` illegal — a browser refuses that pair outright.
//
// Outermost by design, so a preflight to a path nobody routed still answers: the browser then
// reports the 404 the request actually deserves instead of a CORS error that hides it.
func CORS(allowed AllowedOrigins) func(http.Handler) http.Handler {
	anyOrigin := slices.Contains(allowed, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Varied on every reply, preflight or not: a cache that kept one origin's allow
			// header would serve it to the next origin, which the browser then refuses.
			w.Header().Add("Vary", "Origin")
			if origin := r.Header.Get("Origin"); origin != "" && (anyOrigin || slices.Contains(allowed, origin)) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Expose-Headers", corsExposedHeaders)
			}
			// A preflight is answered here and never routed: it carries no token and no body, so
			// letting it reach a handler means an unauthenticated OPTIONS on every route. An
			// origin nobody allowed is still answered — without the allow header, which is the
			// refusal the browser is asking for.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Methods", corsMethods)
				// Echoed rather than listed: the browser names exactly the headers its client set,
				// so a new one (a trace header, an idempotency key) needs no deploy here.
				if asked := r.Header.Get("Access-Control-Request-Headers"); asked != "" {
					w.Header().Set("Access-Control-Allow-Headers", asked)
				}
				w.Header().Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NormalizeOrigins trims each configured entry to the scheme://host[:port] a browser sends. An
// operator who pastes a site URL with its trailing slash or its path would otherwise match
// nothing, and a CORS allowlist that silently matches nothing is the failure this whole file is
// for.
func NormalizeOrigins(raw []string) AllowedOrigins {
	out := make(AllowedOrigins, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			out = append(out, entry)
			continue
		}
		scheme, rest, ok := strings.Cut(entry, "://")
		if !ok {
			// No scheme to keep, so there is nothing to normalise into: pass it through and let
			// it fail to match, rather than inventing https for it.
			out = append(out, entry)
			continue
		}
		host, _, _ := strings.Cut(rest, "/")
		out = append(out, scheme+"://"+host)
	}
	return out
}

package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Outbound instrumentation lives in the HTTP transport rather than in each
// provider's methods: one wrapper covers every call a provider makes, with no
// per-method boilerplate and nothing for a new provider to remember.

// OutboundCall describes one completed outbound HTTP attempt.
type OutboundCall struct {
	// Provider is the logical dependency name, e.g. "litellm", "vnpay".
	Provider string
	Method   string
	Host     string
	// Path excludes the query string, which can carry credentials.
	Path string
	// StatusCode is 0 when no response arrived at all (dial error, timeout,
	// context cancelled).
	StatusCode int
	// Duration is time until the response *headers* arrived — the body is not
	// read yet. For a streamed response this is time-to-first-byte, not the time
	// to the last chunk. That makes it the right latency signal for deciding a
	// dependency is unhealthy, and the wrong one for "how long did generation
	// take".
	Duration time.Duration
	Err      error
}

// Failed reports whether the attempt should count against the dependency: a
// transport error, a 5xx, or a 429. Rate limiting counts because the dependency
// is refusing traffic and backing off is the right response; other 4xx do not,
// because a rejected bad request is a correct answer, and counting those would
// trip a breaker while the provider is perfectly healthy.
func (c OutboundCall) Failed() bool {
	return c.Err != nil || c.StatusCode >= 500 || c.StatusCode == http.StatusTooManyRequests
}

// OutboundObserver receives every attempt. It runs on the calling goroutine, so
// it must be cheap and must not block — buffer and flush elsewhere.
type OutboundObserver func(ctx context.Context, call OutboundCall)

// ObserveOutbound wraps next so every attempt is reported to observe. A nil next
// means http.DefaultTransport; a nil observe returns next unwrapped.
func ObserveOutbound(provider string, next http.RoundTripper, observe OutboundObserver) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if observe == nil {
		return next
	}
	return &observeTransport{provider: provider, next: next, observe: observe}
}

type observeTransport struct {
	provider string
	next     http.RoundTripper
	observe  OutboundObserver
}

func (t *observeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(req)

	call := OutboundCall{
		Provider: t.provider,
		Method:   req.Method,
		Host:     req.URL.Host,
		Path:     req.URL.Path,
		Duration: time.Since(start),
		Err:      err,
	}
	if resp != nil {
		call.StatusCode = resp.StatusCode
	}
	t.observe(req.Context(), call)

	// The response is passed through untouched: reading or closing the body here
	// would break streaming callers.
	return resp, err
}

// LogOutbound reports calls to log — warn for failures, debug otherwise. Useful
// on its own and as a fallback when no metrics sink is wired.
func LogOutbound(log *slog.Logger) OutboundObserver {
	return func(ctx context.Context, call OutboundCall) {
		attrs := []any{
			"provider", call.Provider,
			"method", call.Method,
			"path", call.Path,
			"status", call.StatusCode,
			"duration_ms", float64(call.Duration.Microseconds()) / 1000,
		}
		if call.Err != nil {
			attrs = append(attrs, "err", call.Err)
		}
		if call.Failed() {
			log.WarnContext(ctx, "outbound call failed", attrs...)
			return
		}
		log.DebugContext(ctx, "outbound call", attrs...)
	}
}

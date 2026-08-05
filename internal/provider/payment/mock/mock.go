// Package mock is the dev-only payment rail: no gateway contract, no card, and one option row per
// outcome, so a client can walk a success, a decline, a redirect, a late webhook and an unreachable
// rail without anybody's sandbox credentials. Registered when `payment.providers` names `mock`.
//
// A scenario is chosen by the option slug the payer tendered, because that is the one thing a
// client can pick from a list — and this rail declares those rows itself (`Options`), so the list a
// client sees and the behaviour behind it cannot drift. Whether the rows exist at all is
// `payment.providers`: this provider is registered like any other, and nothing outside it branches on
// being a mock. A slug this table does not know succeeds, which is what an operator's own row
// pointed at this provider gets.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"shopnexus/internal/provider/payment"
)

// Name is what an option row's `provider` says to be served by this rail.
const Name = "mock"

// The option slugs this rail understands, and publishes as its own rows. A slug is permanent once
// published: a settled `transaction.payment_option` holds it as a plain string with no foreign key.
const (
	OptionSuccess         = "mock-success"
	OptionDecline         = "mock-decline"
	OptionSlowSuccess     = "mock-slow-success"
	OptionRedirect        = "mock-redirect"
	OptionWebhookSuccess  = "mock-webhook-success"
	OptionWebhookDecline  = "mock-webhook-decline"
	OptionWebhookRetried  = "mock-webhook-retried"
	OptionWebhookMismatch = "mock-webhook-mismatch"
	OptionUnreachable     = "mock-unreachable"
	OptionNoAnswer        = "mock-no-answer"
)

// webhookDelay is how long a reporting rail takes to call back. Long enough that a client shows
// its pending state, short enough to watch.
const webhookDelay = 8 * time.Second

// chargePause is how long the slow rail holds the request open, which is what exercises a client's
// spinner and its own timeout without a slow network to arrange.
const chargePause = 3 * time.Second

// scenario is what one option row does when it is tendered. Every field is a way a real rail has
// behaved, and the zero value is "pending forever" — a rail that took the request and said nothing.
type scenario struct {
	// decided is the outcome Charge answers with, direct-debit style. Empty leaves the leg
	// pending, which is what every reporting rail does.
	decided payment.Status
	// hosted sends the payer to this rail's own pay page, where they pick the outcome. The only
	// scenario a browser walks end to end, and the shape a real redirect rail has.
	hosted bool
	// reports is what the webhook says once webhookDelay has passed; empty means it never calls.
	reports payment.Status
	// twice delivers the same notification again, because a real gateway retries until it gets a
	// 200 and a client that double-counts the second one double-counts in production.
	twice bool
	// mismatch reports an amount other than the one charged. Finance settles on its own leg, not
	// on the number the rail claims, and that is worth a scenario rather than a comment.
	mismatch bool
	// unreachable makes Charge fail. Not a declined payment: nothing may settle, and the payer has
	// to be able to tender again.
	unreachable bool
	// pause holds Charge open before it answers.
	pause time.Duration
}

var scenarios = map[string]scenario{
	OptionSuccess:         {decided: payment.StatusSuccess},
	OptionDecline:         {decided: payment.StatusFailed},
	OptionSlowSuccess:     {decided: payment.StatusSuccess, pause: chargePause},
	OptionRedirect:        {hosted: true},
	OptionWebhookSuccess:  {reports: payment.StatusSuccess},
	OptionWebhookDecline:  {reports: payment.StatusFailed},
	OptionWebhookRetried:  {reports: payment.StatusSuccess, twice: true},
	OptionWebhookMismatch: {reports: payment.StatusSuccess, mismatch: true},
	OptionUnreachable:     {unreachable: true},
	OptionNoAnswer:        {},
}

// Options are the rows this rail owns. Descriptions included, because the point of a scenario is
// that a human picks it off a list and knows what they are about to get.
//
// Priorities descend from 90 so an operator's own rails (100 by convention) stay above them, and a
// dev stack still reads top-to-bottom as happy path first.
func (c *Client) Options() []payment.Option {
	return []payment.Option{
		{ID: OptionSuccess, Name: "Mock: pay now (succeeds)",
			Description: "Settles immediately, no redirect and no webhook. The happy path.",
			Priority:    90},
		{ID: OptionDecline, Name: "Mock: pay now (declined)",
			Description: "Refused immediately. The session goes back on the shelf, so another rail can be tendered.",
			Priority:    89},
		{ID: OptionSlowSuccess, Name: "Mock: pay now (slow, succeeds)",
			Description: "Holds the request open for a few seconds, then succeeds — a spinner and a client timeout.",
			Priority:    88},
		{ID: OptionRedirect, Name: "Mock: hosted page",
			Description: "Redirects to a fake gateway page where you press Pay or Decline, then comes back. The shape of a real redirect rail.",
			Priority:    87},
		{ID: OptionWebhookSuccess, Name: "Mock: webhook succeeds later",
			Description: "Answers pending and reports success a few seconds later, so a client has to wait for the notification rather than trust the response.",
			Priority:    86},
		{ID: OptionWebhookDecline, Name: "Mock: webhook declines later",
			Description: "Answers pending, then reports a failure. The other half of the asynchronous path.",
			Priority:    85},
		{ID: OptionWebhookRetried, Name: "Mock: webhook delivered twice",
			Description: "Reports the same success twice, the way a real gateway retries until it gets a 200. Nothing may be counted twice.",
			Priority:    84},
		{ID: OptionWebhookMismatch, Name: "Mock: webhook reports another amount",
			Description: "Reports success for half the amount charged. The leg settles on its own figure, never on the one the rail claims.",
			Priority:    83},
		{ID: OptionUnreachable, Name: "Mock: rail is down",
			Description: "The charge call itself fails. Not a declined payment: nothing settles and the payer can try again.",
			Priority:    82},
		{ID: OptionNoAnswer, Name: "Mock: never answers",
			Description: "Answers pending and never reports. The session sits until the expiry job voids it — settle it by hand with POST /api/v1/webhooks/payment/mock.",
			Priority:    81},
	}
}

// deliverHook is finance's settler, captured at WireWebhooks: a delayed report has no request to
// carry it, and there is one payment webhook per process. Dev-only, like the rest of this package.
var (
	hookMu      sync.RWMutex
	deliverHook payment.NotificationHandler
)

// Config is what the hosted page needs to exist as a browser sees it.
type Config struct {
	// BaseURL is where this gateway answers as a client sees it, API base path included, since that
	// is where provider callbacks are mounted. Without it the redirect scenario could only answer a
	// relative path, which a web client on another origin cannot follow.
	BaseURL string
}

var _ payment.Client = (*Client)(nil)

type Client struct {
	baseURL string
	log     *slog.Logger
}

func NewClient(cfg Config, log *slog.Logger) payment.Client {
	return &Client{baseURL: cfg.BaseURL, log: log}
}

// Charge plays the scenario the tendered option names.
func (c *Client) Charge(ctx context.Context, params payment.ChargeParams) (payment.ChargeResult, error) {
	s, ok := scenarios[params.Option]
	if !ok {
		s = scenarios[OptionSuccess]
	}
	if s.pause > 0 {
		select {
		case <-time.After(s.pause):
		case <-ctx.Done():
			return payment.ChargeResult{}, fmt.Errorf("mock rail: %w", ctx.Err())
		}
	}
	if s.unreachable {
		return payment.ChargeResult{}, fmt.Errorf("mock rail %q is unreachable", params.Option)
	}
	providerID := uuid.NewString()
	if s.hosted {
		return payment.ChargeResult{ProviderID: providerID, RedirectURL: c.hostedURL(params)}, nil
	}
	if s.reports != "" {
		c.report(s, params, providerID)
	}
	return payment.ChargeResult{ProviderID: providerID, Status: s.decided}, nil
}

// hostedURL is this rail's own pay page, standing in for a gateway's. The return URL is carried in
// the query because that is where a real gateway keeps it; finance validated it against its
// allowlist before this rail ever saw it.
func (c *Client) hostedURL(params payment.ChargeParams) string {
	q := url.Values{
		"ref":         {params.RefID},
		"amount":      {strconv.FormatInt(params.Amount, 10)},
		"description": {params.Description},
	}
	if params.ReturnURL != "" {
		q.Set("return", params.ReturnURL)
	}
	return c.baseURL + checkoutPath + "?" + q.Encode()
}

// report calls back after webhookDelay, the way a reporting rail does.
func (c *Client) report(s scenario, params payment.ChargeParams, providerID string) {
	amount := params.Amount
	if s.mismatch {
		amount = params.Amount / 2
	}
	n := payment.Notification{
		RefID: params.RefID, Status: s.reports, Amount: amount, ProviderTxID: providerID,
	}
	time.AfterFunc(webhookDelay, func() {
		c.deliver(n)
		if s.twice {
			c.deliver(n)
		}
	})
}

// deliver hands one notification to finance. Best-effort and logged: a mock rail that cannot
// report is a dev stack to look at, not a failure to propagate — there is no caller left.
func (c *Client) deliver(n payment.Notification) {
	hookMu.RLock()
	settle := deliverHook
	hookMu.RUnlock()
	if settle == nil {
		c.log.Warn("mock payment rail has no settler (WireWebhooks not called)", "ref_id", n.RefID)
		return
	}
	if err := settle(context.Background(), n); err != nil {
		c.log.Error("mock payment rail could not settle", "ref_id", n.RefID, "err", err)
	}
}

const (
	// webhookPath is the IPN, kept as a route so a pending leg can be settled by hand:
	// POST /webhooks/payment/mock {"ref_id":"txn_…","status":"success"}. That is the whole
	// answer for `mock-no-answer`, and for a redirect somebody abandoned.
	webhookPath = "/webhooks/payment/mock"
	// checkoutPath is the hosted pay page, decisionPath its form target.
	checkoutPath = "/webhooks/payment/mock/checkout"
	decisionPath = "/webhooks/payment/mock/decision"
)

// WireWebhooks captures the settler and mounts the IPN plus the hosted pay page.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver payment.NotificationHandler) string {
	hookMu.Lock()
	deliverHook = deliver
	hookMu.Unlock()

	mux.HandleFunc("POST "+webhookPath, func(w http.ResponseWriter, r *http.Request) {
		var n payment.Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil || n.RefID == "" {
			http.Error(w, "want {\"ref_id\":…,\"status\":\"success\"|\"failed\"}", http.StatusBadRequest)
			return
		}
		if err := deliver(r.Context(), n); err != nil {
			c.log.Error("mock payment IPN", "ref_id", n.RefID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET "+checkoutPath, c.serveCheckout)
	mux.HandleFunc("POST "+decisionPath, func(w http.ResponseWriter, r *http.Request) {
		ref, status := r.FormValue("ref"), payment.Status(r.FormValue("status"))
		if ref == "" || (status != payment.StatusSuccess && status != payment.StatusFailed) {
			http.Error(w, "pick an outcome", http.StatusBadRequest)
			return
		}
		if err := deliver(r.Context(), payment.Notification{
			RefID: ref, Status: status, ProviderTxID: uuid.NewString(),
		}); err != nil {
			c.log.Error("mock payment decision", "ref_id", ref, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Back to whoever sent the payer here, which is what a gateway does once the payer is
		// done. Nothing else on this page is a redirect target, and the URL was checked against
		// finance's allowlist before it reached this rail.
		if back := r.FormValue("return"); back != "" {
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		_, _ = w.Write([]byte("<!doctype html><p>Recorded. There was no return URL to go back to."))
	})

	return webhookPath
}

func (c *Client) serveCheckout(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("ref") == "" {
		http.Error(w, "no payment reference", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// html/template, not a printf: the amount and the description are the payer's own text coming
	// back through a query string, and this page is served from the platform's own origin.
	if err := checkoutPage.Execute(w, map[string]string{
		"Ref":         q.Get("ref"),
		"Amount":      q.Get("amount"),
		"Description": q.Get("description"),
		"Return":      q.Get("return"),
		"Action":      decisionPath,
	}); err != nil {
		c.log.Error("render mock checkout page", "err", err)
	}
}

// checkoutPage stands in for a gateway's hosted form: it is what makes the redirect scenario
// walkable in a browser, both outcomes from the same page.
var checkoutPage = template.Must(template.New("checkout").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Mock payment</title>
<style>
 body{font:16px/1.5 system-ui,sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem}
 dl{display:grid;grid-template-columns:auto 1fr;gap:.25rem .75rem;margin:1.5rem 0}
 dt{color:#666} form{display:flex;gap:.75rem} button{flex:1;padding:.75rem;font:inherit;cursor:pointer}
 .pay{background:#0a7d32;color:#fff;border:0} .no{background:#fff;border:1px solid #b00}
</style>
<h1>Mock payment rail</h1>
<p>No money moves here. Pick what the gateway should report back.</p>
<dl>
 <dt>Reference</dt><dd><code>{{.Ref}}</code></dd>
 <dt>Amount</dt><dd>{{.Amount}}</dd>
 {{if .Description}}<dt>For</dt><dd>{{.Description}}</dd>{{end}}
</dl>
<form method="post" action="{{.Action}}">
 <input type="hidden" name="ref" value="{{.Ref}}">
 <input type="hidden" name="return" value="{{.Return}}">
 <button class="pay" name="status" value="success">Pay</button>
 <button class="no" name="status" value="failed">Decline</button>
</form>
`))

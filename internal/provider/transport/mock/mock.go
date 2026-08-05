// Package mock is the dev-only courier: no carrier contract, and one option row per way a parcel
// can go, so the whole shipment lifecycle — quoted, booked, in transit, delivered, and the ways it
// does not get there — can be walked without a courier account. Registered when
// `transport.providers` names `mock`.
//
// A scenario is chosen by the carrier slug the buyer picked, which is the one thing a client picks
// from a list — and this courier declares those rows itself (`Options`), so the list a client sees
// and the behaviour behind it cannot drift. Nothing outside this package branches on being a mock. A
// slug this table does not know behaves like standard delivery, which is what an operator's own row
// pointed at this provider gets.
package mock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"shopnexus/internal/provider/transport"
)

// The option slugs this courier understands, and publishes as its own rows. A slug is permanent once
// published: a shipped `order.item.transport_option` holds it as a plain string with no foreign key.
const (
	OptionStandard       = "mock-standard"
	OptionExpress        = "mock-express"
	OptionEconomy        = "mock-economy"
	OptionNoService      = "mock-no-service"
	OptionSlowQuote      = "mock-slow-quote"
	OptionBookingFails   = "mock-booking-fails"
	OptionStuck          = "mock-stuck"
	OptionFailedDelivery = "mock-failed-delivery"
	OptionRetried        = "mock-checkpoints-retried"
	OptionLateCheckpoint = "mock-late-checkpoint"
	OptionUnknownStatus  = "mock-unknown-status"
)

// Name is what an option row's `provider` says to be served by this courier.
const Name = "mock"

// defaultCost is what a slug this package does not define is priced at — an operator's own row
// pointed here. Flat, because this courier does not price by weight.
const defaultCost = 15000 // VND

// defaultDelay is how long that same parcel takes to arrive. Nothing in this package waits longer
// than this: the delays are here to be *watched*, not to stand in for a real transit time, so the
// slowest scenario still finishes inside half a minute.
const defaultDelay = 30 * time.Second

// quotePause is how long the slow courier holds a quote open, which is what exercises a client's
// spinner and its own timeout without a slow network to arrange.
const quotePause = 3 * time.Second

// unknownStatus is a vocabulary this platform does not model. `RecordCarrierCheckpoint` translates
// the three it knows and ignores the rest rather than guessing, and a courier really does report
// things like this — so there is a row that produces one.
const unknownStatus = "held-at-customs"

// scenario is what one carrier row does. The costs differ so a quote list is a real choice rather
// than the same number under three names.
type scenario struct {
	cost int64
	// delay is how long the parcel takes to report delivered, counted from the booking.
	delay time.Duration
	// declines makes Quote fail: this courier does not serve the route. It then goes missing from
	// the quote list rather than failing the page, which is the behaviour worth being able to see.
	declines bool
	// bookingFails makes Create fail. The fee was already collected, so this is the case the
	// unbooked-shipment retry exists for.
	bookingFails bool
	// stalls reports "processing" and nothing after it: a parcel that was accepted and then went
	// quiet, which is what an escrow window has to survive.
	stalls bool
	// undelivered ends at "failed" instead of "success" — the parcel came back.
	undelivered bool
	// quotePause holds Quote open before it answers, so a checkout's spinner and its timeout are
	// exercised. Create is left alone: it runs after the money, where nobody is watching.
	quotePause time.Duration
	// twice reports every checkpoint again, because a carrier retries until it gets a 200 and the
	// forward-only rule has to make the second one a no-op rather than a second advance.
	twice bool
	// late reports an *earlier* checkpoint after the parcel arrived. They do arrive out of order, and
	// nothing may un-deliver a delivered parcel.
	late bool
	// unknown reports a status this platform does not model, which must be ignored rather than
	// guessed at — guessing would advance a parcel on a word nobody agreed the meaning of.
	unknown bool
}

// checkpoints is what this scenario reports, in order, in the carrier's own vocabulary. A plan
// rather than a branch at delivery time, so the odd sequences read as sequences.
func (s scenario) checkpoints() []string {
	const (
		inTransit = string(transport.StatusProcessing)
		delivered = string(transport.StatusSuccess)
		failed    = string(transport.StatusFailed)
	)
	switch {
	case s.stalls:
		return []string{inTransit}
	case s.undelivered:
		return []string{inTransit, failed}
	case s.twice:
		return []string{inTransit, inTransit, delivered, delivered}
	case s.late:
		return []string{inTransit, delivered, inTransit}
	case s.unknown:
		return []string{inTransit, unknownStatus}
	default:
		return []string{inTransit, delivered}
	}
}

var scenarios = map[string]scenario{
	OptionStandard:       {cost: defaultCost, delay: 15 * time.Second},
	OptionExpress:        {cost: 35000, delay: 5 * time.Second},
	OptionEconomy:        {cost: 8000, delay: defaultDelay},
	OptionNoService:      {cost: defaultCost, declines: true},
	OptionSlowQuote:      {cost: defaultCost, delay: 15 * time.Second, quotePause: quotePause},
	OptionBookingFails:   {cost: defaultCost, bookingFails: true},
	OptionStuck:          {cost: defaultCost, delay: 5 * time.Second, stalls: true},
	OptionFailedDelivery: {cost: defaultCost, delay: 15 * time.Second, undelivered: true},
	OptionRetried:        {cost: defaultCost, delay: 5 * time.Second, twice: true},
	OptionLateCheckpoint: {cost: defaultCost, delay: 5 * time.Second, late: true},
	OptionUnknownStatus:  {cost: defaultCost, delay: 5 * time.Second, unknown: true},
}

// Options are the rows this courier owns. Priorities descend from 90 so an operator's own services
// (100 by convention) stay above them, cheapest-and-slowest last.
func (c *Client) Options() []transport.Option {
	return []transport.Option{
		{ID: OptionStandard, Name: "Mock: standard (2 days)",
			Description: "Delivers about fifteen seconds after booking. The happy path.",
			Priority:    90},
		{ID: OptionExpress, Name: "Mock: express (same day)",
			Description: "Costs more and delivers in about five seconds, so the whole escrow lifecycle is watchable.",
			Priority:    89},
		{ID: OptionEconomy, Name: "Mock: economy (a week)",
			Description: "Cheapest, and the slowest this courier goes: about thirty seconds, long enough to see an order sit in transit.",
			Priority:    88},
		{ID: OptionNoService, Name: "Mock: does not serve this route",
			Description: "Refuses to quote, so it is missing from the shipping-quote list instead of failing the page. Choosing it at checkout is refused.",
			Priority:    87},
		{ID: OptionSlowQuote, Name: "Mock: slow to quote",
			Description: "Holds the quote open for a few seconds before answering — a spinner at checkout, and a client timeout.",
			Priority:    86},
		{ID: OptionBookingFails, Name: "Mock: booking is refused",
			Description: "Quotes and takes the fee, then refuses the booking. The case the unbooked-shipment retry exists for.",
			Priority:    85},
		{ID: OptionStuck, Name: "Mock: parcel goes quiet",
			Description: "Reports in-transit and never anything else. Move it along yourself at /api/v1/webhooks/transport/mock/console?tracking_id=…",
			Priority:    84},
		{ID: OptionFailedDelivery, Name: "Mock: delivery fails",
			Description: "Goes in-transit and then comes back undelivered.",
			Priority:    83},
		{ID: OptionRetried, Name: "Mock: every checkpoint reported twice",
			Description: "Reports each checkpoint again, the way a carrier retries until it gets a 200. The second one must change nothing.",
			Priority:    82},
		{ID: OptionLateCheckpoint, Name: "Mock: a checkpoint arrives late",
			Description: "Delivers the parcel and then reports in-transit again. They do arrive out of order, and nothing may un-deliver a delivered parcel.",
			Priority:    81},
		{ID: OptionUnknownStatus, Name: "Mock: reports a status we do not model",
			Description: "Goes in-transit, then reports \"held-at-customs\". A word nobody agreed the meaning of is ignored, not guessed at.",
			Priority:    80},
	}
}

// deliverHook is the app's transport ResultHandler, captured at WireWebhooks and shared across
// instances: a delayed checkpoint has no request to carry it, and there is one transport webhook
// deliver per process. Dev-only, like the rest of this package.
var (
	hookMu      sync.RWMutex
	deliverHook transport.ResultHandler
)

var _ transport.Client = (*Client)(nil)

type Client struct{ log *slog.Logger }

func NewClient(log *slog.Logger) transport.Client { return &Client{log: log} }

func scenarioFor(option string) scenario {
	if s, ok := scenarios[option]; ok {
		return s
	}
	return scenario{cost: defaultCost, delay: defaultDelay}
}

func (c *Client) Quote(ctx context.Context, params transport.QuoteParams) (transport.QuoteResult, error) {
	s := scenarioFor(params.Option)
	if s.quotePause > 0 {
		select {
		case <-time.After(s.quotePause):
		case <-ctx.Done():
			return transport.QuoteResult{}, fmt.Errorf("mock courier: %w", ctx.Err())
		}
	}
	if s.declines {
		return transport.QuoteResult{}, fmt.Errorf("mock courier %q does not serve this route", params.Option)
	}
	return transport.QuoteResult{Cost: s.cost, Data: json.RawMessage(`{}`)}, nil
}

// Create books a shipment: stamps a tracking id into Data and schedules the checkpoints the chosen
// scenario reports.
func (c *Client) Create(_ context.Context, params transport.CreateParams) (transport.Transport, error) {
	s := scenarioFor(params.Option)
	if s.bookingFails {
		return transport.Transport{}, fmt.Errorf("mock courier %q refused the booking", params.Option)
	}
	trackingID := newTrackingID()
	data, err := json.Marshal(map[string]string{"tracking_id": trackingID})
	if err != nil {
		return transport.Transport{}, fmt.Errorf("marshal mock shipment data: %w", err)
	}

	c.scheduleCheckpoints(s, trackingID)

	return transport.Transport{
		ID:     trackingID,
		Option: params.Option,
		Cost:   s.cost,
		Data:   data,
	}, nil
}

// scheduleCheckpoints reports the scenario's sequence once the delay has passed.
func (c *Client) scheduleCheckpoints(s scenario, trackingID string) {
	plan := s.checkpoints()
	time.AfterFunc(s.delay, func() { c.reportPlan(plan, trackingID) })
}

// reportPlan walks a sequence of checkpoints. Its own method so the walk can be exercised without
// waiting on a clock — the delays here exist to be watched, not asserted on.
func (c *Client) reportPlan(plan []string, trackingID string) {
	for _, status := range plan {
		// A report the order module refused is worth stopping for — the rest of the plan would only
		// repeat the same failure — but not worth retrying: nothing here is the record of anything.
		if err := c.report(trackingID, status); err != nil {
			return
		}
	}
}

// report hands one checkpoint to the order module.
func (c *Client) report(trackingID, status string) error {
	hookMu.RLock()
	deliver := deliverHook
	hookMu.RUnlock()
	if deliver == nil {
		c.log.Warn("mock courier has no deliver hook (WireWebhooks not called)", "tracking_id", trackingID)
		return errNoHook
	}
	err := deliver(context.Background(), transport.WebhookResult{
		TransportID: trackingID,
		Status:      status,
		Data:        map[string]any{"tracking_id": trackingID, "mock": "scheduled"},
	})
	if err != nil {
		c.log.Error("mock courier could not report",
			"status", status, "tracking_id", trackingID, "err", err)
		return err
	}
	return nil
}

// errNoHook is a wiring mistake rather than a carrier problem: nothing captured the settler, so no
// checkpoint can reach the order module at all.
var errNoHook = errors.New("mock courier: no deliver hook captured")

// The routes this courier mounts, under the prefix the router serves the webhook mux at.
const (
	// webhookPath is the JSON report, for a script.
	webhookPath = "/webhooks/transport/mock"
	// consolePath is the same thing for a person, and decisionPath its form target. The parcel that
	// goes quiet used to say "move it along by hand with POST" — which meant curl, and a scenario
	// nobody exercises. This is the mock payment rail's hosted page, for a courier.
	consolePath  = "/webhooks/transport/mock/console"
	decisionPath = "/webhooks/transport/mock/decision"
)

// WireWebhooks captures the deliver hook and mounts the two ways a checkpoint is reported by hand:
// a JSON route for a script, and a page for a person. Both exist for the same reason — a scenario
// that stalls, or one you want moved along faster than its own clock.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver transport.ResultHandler) string {
	hookMu.Lock()
	deliverHook = deliver
	hookMu.Unlock()

	report := func(ctx context.Context, trackingID, status string) error {
		return deliver(ctx, transport.WebhookResult{
			TransportID: trackingID,
			Status:      status,
			Data:        map[string]any{"tracking_id": trackingID, "mock": "by-hand"},
		})
	}

	mux.HandleFunc("POST "+webhookPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TrackingID string `json:"tracking_id"`
			Status     string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TrackingID == "" {
			http.Error(w, `want {"tracking_id":"MOCK…","status":"processing"|"success"|"failed"}`,
				http.StatusBadRequest)
			return
		}
		if err := report(r.Context(), body.TrackingID, body.Status); err != nil {
			// Answered rather than swallowed: this route used to log the failure and reply 200, so a
			// checkpoint that never landed looked exactly like one that did.
			c.log.Error("mock courier report by hand", "tracking_id", body.TrackingID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET "+consolePath, c.serveConsole)
	mux.HandleFunc("POST "+decisionPath, func(w http.ResponseWriter, r *http.Request) {
		trackingID, status := r.FormValue("tracking_id"), r.FormValue("status")
		if trackingID == "" || carrierStatuses[status] == "" {
			http.Error(w, "pick a checkpoint", http.StatusBadRequest)
			return
		}
		if err := report(r.Context(), trackingID, status); err != nil {
			c.log.Error("mock courier console", "tracking_id", trackingID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Back to the console, so the next checkpoint is one more click: a parcel is walked through
		// several of these, unlike a payment, which is decided once.
		http.Redirect(w, r, consolePath+"?tracking_id="+url.QueryEscape(trackingID)+
			"&reported="+url.QueryEscape(status), http.StatusSeeOther)
	})

	return webhookPath
}

// carrierStatuses is what the console offers, and the check that a form value is one of them: the
// three this platform models, in the order a parcel meets them.
var carrierStatuses = map[string]string{
	string(transport.StatusProcessing): "In transit",
	string(transport.StatusSuccess):    "Delivered",
	string(transport.StatusFailed):     "Failed / returned",
}

// consoleOrder keeps the buttons in the order a parcel moves through them; a map would shuffle them
// on every render.
var consoleOrder = []transport.Status{
	transport.StatusProcessing, transport.StatusSuccess, transport.StatusFailed,
}

func (c *Client) serveConsole(w http.ResponseWriter, r *http.Request) {
	trackingID := r.URL.Query().Get("tracking_id")
	if trackingID == "" {
		http.Error(w, "no tracking id (GET /orders/{id}/transport has it)", http.StatusBadRequest)
		return
	}
	type button struct{ Value, Label string }
	buttons := make([]button, 0, len(consoleOrder))
	for _, status := range consoleOrder {
		buttons = append(buttons, button{string(status), carrierStatuses[string(status)]})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := consolePage.Execute(w, map[string]any{
		"TrackingID": trackingID,
		"Reported":   r.URL.Query().Get("reported"),
		"Action":     decisionPath,
		"Buttons":    buttons,
	}); err != nil {
		c.log.Error("render mock courier console", "err", err)
	}
}

// consolePage is the mock payment rail's hosted page, for a parcel: one button per checkpoint this
// platform models. html/template escapes the tracking id, which arrives through a query string.
var consolePage = template.Must(template.New("console").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Mock courier</title>
<style>
 body{font:16px/1.5 system-ui,sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem}
 form{display:flex;flex-direction:column;gap:.5rem;margin-top:1.5rem}
 button{padding:.75rem;font:inherit;cursor:pointer}
 .done{background:#e8f5e9;border:1px solid #0a7d32;padding:.5rem .75rem;border-radius:.25rem}
</style>
<h1>Mock courier</h1>
<p>No parcel moves here. Report a checkpoint for <code>{{.TrackingID}}</code>.</p>
{{if .Reported}}<p class="done">Reported <strong>{{.Reported}}</strong>.</p>{{end}}
<form method="post" action="{{.Action}}">
 <input type="hidden" name="tracking_id" value="{{.TrackingID}}">
 {{range .Buttons}}<button name="status" value="{{.Value}}">{{.Label}}</button>
 {{end}}</form>
<p><small>The forward-only rule still applies: a checkpoint behind where the parcel already is
is accepted and dropped.</small></p>
`))

func newTrackingID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "MOCK" + strings.ToUpper(hex.EncodeToString(b))
}

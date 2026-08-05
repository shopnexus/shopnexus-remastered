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
	"fmt"
	"log/slog"
	"net/http"
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
	OptionBookingFails   = "mock-booking-fails"
	OptionStuck          = "mock-stuck"
	OptionFailedDelivery = "mock-failed-delivery"
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
}

var scenarios = map[string]scenario{
	OptionStandard:       {cost: defaultCost, delay: 15 * time.Second},
	OptionExpress:        {cost: 35000, delay: 5 * time.Second},
	OptionEconomy:        {cost: 8000, delay: defaultDelay},
	OptionNoService:      {cost: defaultCost, declines: true},
	OptionBookingFails:   {cost: defaultCost, bookingFails: true},
	OptionStuck:          {cost: defaultCost, delay: 5 * time.Second, stalls: true},
	OptionFailedDelivery: {cost: defaultCost, delay: 15 * time.Second, undelivered: true},
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
		{ID: OptionBookingFails, Name: "Mock: booking is refused",
			Description: "Quotes and takes the fee, then refuses the booking. The case the unbooked-shipment retry exists for.",
			Priority:    86},
		{ID: OptionStuck, Name: "Mock: parcel goes quiet",
			Description: "Reports in-transit and never anything else. Move it along by hand with POST /api/v1/webhooks/transport/mock.",
			Priority:    85},
		{ID: OptionFailedDelivery, Name: "Mock: delivery fails",
			Description: "Goes in-transit and then comes back undelivered, about fifteen seconds after booking.",
			Priority:    84},
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

func (c *Client) Quote(_ context.Context, params transport.QuoteParams) (transport.QuoteResult, error) {
	s := scenarioFor(params.Option)
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

// scheduleCheckpoints reports processing, then the scenario's ending — or nothing after
// processing, for the parcel that goes quiet.
func (c *Client) scheduleCheckpoints(s scenario, trackingID string) {
	statuses := []transport.Status{transport.StatusProcessing, transport.StatusSuccess}
	switch {
	case s.stalls:
		statuses = statuses[:1]
	case s.undelivered:
		statuses[1] = transport.StatusFailed
	}
	time.AfterFunc(s.delay, func() {
		for _, status := range statuses {
			if !c.deliver(trackingID, status) {
				return
			}
		}
	})
}

// deliver hands one checkpoint to the order module, and answers whether to keep going.
func (c *Client) deliver(trackingID string, status transport.Status) bool {
	hookMu.RLock()
	report := deliverHook
	hookMu.RUnlock()
	if report == nil {
		c.log.Warn("mock courier has no deliver hook (WireWebhooks not called)", "tracking_id", trackingID)
		return false
	}
	err := report(context.Background(), transport.WebhookResult{
		TransportID: trackingID,
		Status:      string(status),
		Data:        map[string]any{"tracking_id": trackingID, "mock": "scheduled"},
	})
	if err != nil {
		c.log.Error("mock courier could not report", "status", status, "tracking_id", trackingID, "err", err)
		return false
	}
	return true
}

// webhookPath is where a carrier report arrives. Under `/webhooks/` because that is the prefix the
// router mounts this mux at — the path it was registered under before matched nothing, so the
// manual trigger this comment advertises could never be called.
const webhookPath = "/webhooks/transport/mock"

// WireWebhooks captures the deliver hook and mounts the manual-trigger route dev uses:
// POST /webhooks/transport/mock {"tracking_id":...,"status":...}. That is the whole answer for the
// parcel that stalls, and for moving one along faster than its scenario would.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver transport.ResultHandler) string {
	hookMu.Lock()
	deliverHook = deliver
	hookMu.Unlock()

	mux.HandleFunc("POST "+webhookPath, func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			TrackingID string `json:"tracking_id"`
			Status     string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.TrackingID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := deliver(r.Context(), transport.WebhookResult{
			TransportID: p.TrackingID,
			Status:      p.Status,
			Data:        map[string]any{"tracking_id": p.TrackingID},
		}); err != nil {
			c.log.Error("mock courier manual deliver", "err", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	return webhookPath
}

func newTrackingID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "MOCK" + strings.ToUpper(hex.EncodeToString(b))
}

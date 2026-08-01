// Package mock is a dev-only transport provider: every booked shipment
// auto-delivers after a fixed delay (default 30s), driving the order webhook
// pipeline without a real carrier. Select it with option Provider="mock".
package mock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"shopnexus/internal/provider"
	"shopnexus/internal/provider/transport"
)

const (
	defaultDelay = 30 * time.Second
	flatCost     = 15000 // VND, flat — mock doesn't price by weight
)

// deliverHook is the app's transport ResultHandler, captured at WireWebhooks and
// shared across mock instances. Dev-only — one transport webhook deliver exists
// per process.
var (
	hookMu      sync.RWMutex
	deliverHook transport.ResultHandler
)

// Data is the provider config carried in provider.Option.Data.
type Data struct {
	DelaySeconds int `json:"delay_seconds"` // auto-deliver delay; 0 => 30s
}

var _ transport.Client = (*Client)(nil)

type Client struct {
	config provider.Option
	delay  time.Duration
}

func NewClient(cfg provider.Option) transport.Client {
	delay := defaultDelay
	if len(cfg.Data) > 0 {
		var d Data
		if json.Unmarshal(cfg.Data, &d) == nil && d.DelaySeconds > 0 {
			delay = time.Duration(d.DelaySeconds) * time.Second
		}
	}
	return &Client{config: cfg, delay: delay}
}

func (c *Client) Config() provider.Option { return c.config }

func (c *Client) Quote(_ context.Context, _ transport.QuoteParams) (transport.QuoteResult, error) {
	return transport.QuoteResult{Cost: flatCost, Data: json.RawMessage(`{}`)}, nil
}

// Create books a shipment: stamps a tracking id into Data and schedules an
// automatic Processing -> Success delivery after the configured delay.
func (c *Client) Create(_ context.Context, params transport.CreateParams) (transport.Transport, error) {
	trackingID := newTrackingID()
	data, _ := json.Marshal(map[string]string{"tracking_id": trackingID})

	c.scheduleDelivery(trackingID)

	return transport.Transport{
		ID:     trackingID,
		Option: params.Option,
		Cost:   flatCost,
		Data:   data,
	}, nil
}

// scheduleDelivery fires Processing then Success after the delay.
func (c *Client) scheduleDelivery(trackingID string) {
	time.AfterFunc(c.delay, func() {
		hookMu.RLock()
		deliver := deliverHook
		hookMu.RUnlock()
		if deliver == nil {
			slog.Warn("mock transport: no deliver hook (WireWebhooks not called)", slog.String("tracking_id", trackingID))
			return
		}
		for _, status := range []string{string(transport.StatusProcessing), string(transport.StatusSuccess)} {
			if err := deliver(context.Background(), transport.WebhookResult{
				TransportID: trackingID,
				Status:      status,
				Data:        map[string]any{"tracking_id": trackingID, "mock": "auto-delivered"},
			}); err != nil {
				slog.Error("mock transport: deliver failed", slog.String("status", status), slog.Any("error", err))
				return
			}
		}
	})
}

func (c *Client) Track(_ context.Context, _ string) (transport.TrackResult, error) {
	return transport.TrackResult{Status: string(transport.StatusSuccess), Data: json.RawMessage(`{}`)}, nil
}

func (c *Client) Cancel(_ context.Context, _ string) error { return nil }

// WireWebhooks captures the deliver hook and mounts a manual-trigger route for
// dev: POST /api/v1/transport/webhook/mock {"tracking_id":...,"status":...}.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver transport.ResultHandler) string {
	const key = "transport/mock"
	hookMu.Lock()
	deliverHook = deliver
	hookMu.Unlock()

	mux.HandleFunc("POST /api/v1/transport/webhook/mock", func(w http.ResponseWriter, r *http.Request) {
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
			slog.Error("mock transport: manual deliver", slog.Any("error", err))
		}
		w.WriteHeader(http.StatusOK)
	})
	return key
}

func newTrackingID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "MOCK" + strings.ToUpper(hex.EncodeToString(b))
}

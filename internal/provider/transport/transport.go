// Package transport defines the shipping/transport provider interface and
// types. Ported from the legacy server; webhook wiring is adapted to net/http.
package transport

import (
	"context"
	"encoding/json"
	"net/http"

	"shopnexus/internal/provider"
)

// WebhookResult contains a verified webhook/tracking update from a transport
// provider. Status is a string to avoid coupling to a module's enum.
type WebhookResult struct {
	TransportID string         // provider's tracking id
	Status      string         // e.g. "InTransit", "Delivered"
	Data        map[string]any // raw provider event data
}

// ResultHandler is called after webhook verification with a parsed result.
type ResultHandler func(ctx context.Context, result WebhookResult) error

type Client interface {
	Config() provider.Option
	Quote(ctx context.Context, params QuoteParams) (QuoteResult, error)
	Create(ctx context.Context, params CreateParams) (Transport, error)
	Track(ctx context.Context, id string) (TrackResult, error)
	Cancel(ctx context.Context, id string) error

	// WireWebhooks mounts the provider's webhook route on mux
	WireWebhooks(mux *http.ServeMux, deliver ResultHandler) string
}

type QuoteParams struct {
	Items       []ItemMetadata
	FromAddress string
	ToAddress   string
}

type ItemMetadata struct {
	VariantID      int64
	Quantity       int64
	PackageDetails json.RawMessage
}

type CreateParams struct {
	Items       []ItemMetadata
	FromAddress string
	ToAddress   string
	Option      string
}

type QuoteResult struct {
	Cost int64
	Data json.RawMessage
}

type Transport struct {
	// ID is the carrier's own reference for the shipment, not a key of ours: a courier hands
	// back whatever string it uses, and this platform's keys are BIGINT.
	ID     string
	Option string
	Cost   int64
	Data   json.RawMessage
}

type TrackResult struct {
	Status string
	Data   json.RawMessage
}

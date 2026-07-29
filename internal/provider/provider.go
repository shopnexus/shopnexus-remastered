// Package provider holds shared types for external service providers
// (payment, transport, ...). Kept separate so provider interfaces can share
// the Option config type without importing a heavier module.
package provider

import (
	"encoding/json"

	"github.com/google/uuid"
)

type OptionType string

const (
	OptionTypePayment   OptionType = "payment"
	OptionTypeTransport OptionType = "transport"
)

// Option is the configuration for a provider option — a specific way to pay or
// ship within a provider. Shared by the payment and transport interfaces.
type Option struct {
	ID       string        `json:"id"`       // e.g. "vnpay-qr", "ghtk-express"
	OwnerID  uuid.NullUUID `json:"owner_id"` // null => provided by us; else user-provided
	Type     OptionType    `json:"type"`     // "payment" | "transport"
	Provider string        `json:"provider"` // "vnpay", "sepay", "ghtk", "mock", ...

	IsEnabled   bool            `json:"is_enabled"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Priority    int32           `json:"priority"`
	Data        json.RawMessage `json:"data"` // provider-specific config
}

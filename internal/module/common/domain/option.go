package domain

// Option types (kebab-case), the high-level grouping of a pluggable integration.
const (
	OptionTypePayment      = "payment"
	OptionTypeTransport    = "transport"
	OptionTypeNotification = "notification"
)

// Option is a pluggable service integration selectable at checkout or
// configuration time (a payment rail, a carrier, …). Rows are seeded/managed by
// operators, so there is no constructor here — the module only reads them.
type Option struct {
	// ID is a natural key ('stripe-xxx', 'ghn-xxx'), not a surrogate one, so it
	// stays a string and is never encoded.
	ID             string
	OwnerID        *int64 // nil for system-provided options
	IsEnabled      bool
	Name           string
	Description    string
	Priority       int
	LogoResourceID *int64
	Data           []byte // JSON: provider-specific configuration
	Type           string
	Provider       string
}

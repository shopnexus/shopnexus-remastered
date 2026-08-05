package common

// Option types (kebab-case), the high-level grouping of a pluggable integration. Each is owned by
// the module that acts on it: payment by finance, carriers by order. A module that grows its own
// kind of pluggable adds it here.
const (
	OptionTypePayment   = "payment"
	OptionTypeTransport = "transport"
)

// OptionProviderPlatform is the `provider` of a row that means "whatever this deployment is
// configured for" — the vendor is `PAYMENT_PROVIDER`/`TRANSPORT_PROVIDER`, not the row's business.
// Every other value names one vendor, and a module offers such a row only when that vendor is the
// one selected: a row claiming a rail the stack cannot reach is a checkout that fails at the last
// step, and the mock scenarios below it must not appear in a deployment charging real cards.
const OptionProviderPlatform = "platform"

// Offered answers whether this row belongs in the list a client picks from, given the provider this
// deployment actually configured.
func (o Option) Offered(configured string) bool {
	return o.Provider == OptionProviderPlatform || o.Provider == configured
}

// Option is a pluggable service integration selectable at checkout or configuration time (a
// payment rail, a carrier, …). Rows are seeded and managed by operators, so there is no
// constructor: a module only reads them.
type Option struct {
	// ID is a natural key ('stripe-xxx', 'ghn-xxx'), not a surrogate one, so it stays a string
	// and is never encoded.
	ID             string
	OwnerID        *int64 // nil for a system-provided option
	IsEnabled      bool
	Name           string
	Description    string
	Priority       int
	LogoResourceID *int64
	Data           []byte // JSON: provider-specific configuration
	Type           string
	Provider       string
}

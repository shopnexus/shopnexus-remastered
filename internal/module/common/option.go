package common

import "context"

// OptionStore is the registry a module reads and an admin edits. Declared here, not in each
// module's `port`, for the same reason `Uploads` is: the table is shared DDL, `dbx.Options`
// is the single implementation, and two modules had begun keeping identical copies of it.
type OptionStore interface {
	// ListEnabled is what a client picks from; ListAll is the staff view, which includes the
	// rows somebody switched off — that being the answer to "why is this carrier missing".
	ListEnabled(ctx context.Context, category string) ([]Option, error)
	ListAll(ctx context.Context, category string) ([]Option, error)
	Find(ctx context.Context, id string) (Option, error)
	Save(ctx context.Context, o Option) error
	// Reconcile makes the rows of one provider exactly `want`, so a provider that declares its
	// own rows owns them and a row it dropped does not linger.
	Reconcile(ctx context.Context, provider string, want []Option) error
}

// Option categories, kebab-case. A category is a kind of pluggable choice, owned by the module that
// acts on it: payment by finance, carriers by order. A module that grows its own kind adds it here
// and says whether a user may see it.
const (
	CategoryPayment   = "payment"
	CategoryTransport = "transport"
)

// userVisibleCategories are the ones a buyer picks from at checkout. Everything else is operator
// configuration: it names the vendors this platform pays and the credentials they sit behind, which
// is a map of the business nobody outside it needs. A category absent from this set is staff-only,
// so adding one is a decision made here rather than in a handler.
var userVisibleCategories = map[string]bool{
	CategoryPayment:   true,
	CategoryTransport: true,
}

// CategoryVisibleTo reports whether a category may be listed by a caller who is not staff. An
// unknown category answers false: a client asking for something nobody defined gets the staff
// refusal rather than an empty list that looks like an answer.
func CategoryVisibleTo(category string, staff bool) bool {
	return staff || userVisibleCategories[category]
}

// Option is a pluggable service integration selectable at checkout or configuration time (a
// payment rail, a carrier, …). Rows are managed by operators, so there is no constructor: a module
// reads them and an admin edits them.
type Option struct {
	// ID is a natural key ('ghn-express', 'vnpay-qr'), not a surrogate one, so it stays a string
	// and is never encoded. Permanent once published: a settled payment and a shipped parcel hold
	// it as a plain string with no foreign key.
	ID             string
	OwnerID        *int64 // nil for a system-provided option
	IsEnabled      bool
	Name           string
	Description    string
	Priority       int
	LogoResourceID *int64
	Data           []byte // JSON: provider-specific configuration
	// Category is the kind of choice this is (`payment`, `transport`). The column is still called
	// `type`, which is the SQL keyword-adjacent name the table was created with.
	Category string
	// Provider is which implementation serves this row — the key the module's provider registry
	// resolves to a client. It is what an admin changes to move a carrier from GHN to GHTK
	// without touching a deployment: the slug, and every order naming it, stay as they are.
	Provider string
}

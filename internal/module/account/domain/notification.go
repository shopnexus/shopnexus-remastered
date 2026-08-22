package domain

import "time"

// Category is what a notification is about, and doubles as the preference key.
type Category string

const (
	CategoryOrder     Category = "order"
	CategoryPromotion Category = "promotion"
	CategorySystem    Category = "system"
	CategoryChat      Category = "chat"
	CategorySocial    Category = "social"
)

// Channel is how it goes out. 'in-app' is this module's own table; the rest are a
// Restate workflow's problem.
type Channel string

const (
	ChannelInApp Channel = "in-app"
	ChannelPush  Channel = "push"
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
)

// Categories and Channels are the full axes of the preference matrix, in the order
// the API reports them. Being explicit lists rather than derived keeps the response
// order stable for a client that renders a grid.
// Not every category has a kind that emits it yet: 'promotion' waits on a campaign tool, and
// 'chat' is deliberately empty — a message already has its own unread badge, and mirroring it
// here would double every conversation. Both stay on the axis because the axis is the product's
// channel matrix, and an account may opt out of a category before anything sends it.
var (
	Categories = []Category{CategoryOrder, CategoryPromotion, CategorySystem, CategoryChat, CategorySocial}
	Channels   = []Channel{ChannelInApp, ChannelPush, ChannelEmail, ChannelSMS}
)

// Notification is what the user was told, once, whatever channels carried it.
//
// It stores the *fact* and not the sentence: Kind names what happened and Payload carries the
// particulars, so the words are chosen when the row is read, in the reader's own language. A
// stored title would be frozen in whatever language the emitter happened to be written in —
// which is how an English "Order placed" ended up in a Vietnamese feed.
type Notification struct {
	ID        int64
	AccountID int64
	Kind      Kind
	// Category is derived from Kind and stored anyway, because it is what the feed filters and
	// the unread counts group by: deriving it in SQL would mean teaching Postgres the
	// vocabulary, and re-deriving it in Go would mean reading rows only to throw them away.
	Category Category
	// Payload is the facts the copy and the link are rendered from — an order id, a total, a
	// moderator's note. Never a sentence: what a fact reads like is the copybook's business.
	Payload   map[string]any
	CreatedAt time.Time
	ReadAt    *time.Time
	// ScheduledAt is a future dispatch time; nil means it went out immediately.
	ScheduledAt *time.Time
}

// Preference is one stored deviation from the default. Rows are sparse: one exists
// only where the account differs, so "no row" means the default.
type Preference struct {
	AccountID int64
	Category  Category
	Channel   Channel
	IsEnabled bool
}

// EffectivePreference is a resolved pair — the value in force plus whether it came
// from the default, so a client can tell an explicit choice from an inherited one.
type EffectivePreference struct {
	Category  Category
	Channel   Channel
	IsEnabled bool
	IsDefault bool
}

// defaultPreferences is the product rule, and it lives here rather than in the
// database so it can change without a migration.
//
// In-app is on for everything: it is a list the user chose to open, so it costs
// them nothing. Push is on where the notice is about their own transaction or
// someone talking to them, and off for promotion, which is the one people leave
// over. Email carries what needs a paper trail. SMS is off everywhere — it costs
// money per message and interrupts hardest, so it is opt-in only.
var defaultPreferences = map[Category]map[Channel]bool{
	CategoryOrder:     {ChannelInApp: true, ChannelPush: true, ChannelEmail: true, ChannelSMS: false},
	CategoryPromotion: {ChannelInApp: true, ChannelPush: false, ChannelEmail: false, ChannelSMS: false},
	CategorySystem:    {ChannelInApp: true, ChannelPush: true, ChannelEmail: true, ChannelSMS: false},
	CategoryChat:      {ChannelInApp: true, ChannelPush: true, ChannelEmail: false, ChannelSMS: false},
	CategorySocial:    {ChannelInApp: true, ChannelPush: true, ChannelEmail: false, ChannelSMS: false},
}

// DefaultPreference reports whether a pair is on when the account has said nothing.
func DefaultPreference(c Category, ch Channel) bool {
	return defaultPreferences[c][ch]
}

// ResolvePreferences overlays the stored rows on the defaults and returns every
// pair. The client gets the whole matrix so it never has to know the defaults.
func ResolvePreferences(stored []Preference) []EffectivePreference {
	set := make(map[Category]map[Channel]bool, len(stored))
	for _, p := range stored {
		if set[p.Category] == nil {
			set[p.Category] = map[Channel]bool{}
		}
		set[p.Category][p.Channel] = p.IsEnabled
	}
	out := make([]EffectivePreference, 0, len(Categories)*len(Channels))
	for _, c := range Categories {
		for _, ch := range Channels {
			if v, ok := set[c][ch]; ok {
				out = append(out, EffectivePreference{Category: c, Channel: ch, IsEnabled: v, IsDefault: false})
				continue
			}
			out = append(out, EffectivePreference{Category: c, Channel: ch, IsEnabled: DefaultPreference(c, ch), IsDefault: true})
		}
	}
	return out
}

// Enabled answers whether one category/channel pair is on, given the sparse stored
// rows: no row means the product default.
func Enabled(stored []Preference, c Category, ch Channel) bool {
	for _, p := range stored {
		if p.Category == c && p.Channel == ch {
			return p.IsEnabled
		}
	}
	return DefaultPreference(c, ch)
}

// SplitPreferences sorts a requested change into rows to store and rows to delete:
// a pair set back to its default deletes the row rather than storing the default
// again, which is what keeps the table sparse and the defaults changeable.
func SplitPreferences(want []Preference) (store, remove []Preference) {
	for _, p := range want {
		if p.IsEnabled == DefaultPreference(p.Category, p.Channel) {
			remove = append(remove, p)
			continue
		}
		store = append(store, p)
	}
	return store, remove
}

// NewNotificationParams is a struct rather than positional arguments: an account id and a
// schedule are both easy to transpose, and there is no Category — it follows from the Kind.
type NewNotificationParams struct {
	AccountID int64
	Kind      Kind
	Payload   map[string]any
	// ScheduledAt is a future dispatch time; nil means it goes out now.
	ScheduledAt *time.Time
}

// NewNotification validates a notification, derives its category from its kind and stamps its
// creation instant.
//
// An unknown kind is refused here rather than defaulted, because every question asked later —
// which category, which words, which page, which letter — is answered from the kind, and a row
// nobody has copy for is a blank line in somebody's feed.
func NewNotification(p NewNotificationParams) (Notification, error) {
	spec, ok := SpecOf(p.Kind)
	if p.AccountID == 0 || !ok {
		return Notification{}, ErrNotificationInvalid
	}
	return Notification{
		AccountID:   p.AccountID,
		Kind:        p.Kind,
		Category:    spec.Category,
		Payload:     p.Payload,
		CreatedAt:   time.Now(),
		ScheduledAt: p.ScheduledAt,
	}, nil
}

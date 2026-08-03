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
var (
	Categories = []Category{CategoryOrder, CategoryPromotion, CategorySystem, CategoryChat, CategorySocial}
	Channels   = []Channel{ChannelInApp, ChannelPush, ChannelEmail, ChannelSMS}
)

// Notification is what the user was told, once, whatever channels carried it.
// It has no opaque id on the wire: the table is chunked by time and is read as a
// feed, so a row is addressed by its instant and never individually.
type Notification struct {
	ID        int64
	AccountID int64
	Category  Category
	Title     string
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

// NewNotificationParams is a struct rather than positional arguments: four of the
// fields are strings or maps and would transpose without a compile error.
type NewNotificationParams struct {
	AccountID int64
	Category  Category
	Title     string
	Payload   map[string]any
	// ScheduledAt is a future dispatch time; nil means it goes out now.
	ScheduledAt *time.Time
}

// NewNotification validates a notification and stamps its creation instant.
func NewNotification(p NewNotificationParams) (Notification, error) {
	if p.AccountID == 0 || p.Title == "" || !validCategory(p.Category) {
		return Notification{}, ErrNotificationInvalid
	}
	return Notification{
		AccountID:   p.AccountID,
		Category:    p.Category,
		Title:       p.Title,
		Payload:     p.Payload,
		CreatedAt:   time.Now(),
		ScheduledAt: p.ScheduledAt,
	}, nil
}

func validCategory(c Category) bool {
	for _, known := range Categories {
		if known == c {
			return true
		}
	}
	return false
}

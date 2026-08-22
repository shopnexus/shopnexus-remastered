package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
)

// The stored rows are sparse, so the client is handed the whole matrix with the defaults
// already applied — otherwise it would have to know them, and they change without a
// migration.
func TestResolvePreferences_CoversEveryPairAndMarksDefaults(t *testing.T) {
	stored := []domain.Preference{
		{Category: domain.CategoryOrder, Channel: domain.ChannelPush, IsEnabled: false},
	}
	got := domain.ResolvePreferences(stored)

	if want := len(domain.Categories) * len(domain.Channels); len(got) != want {
		t.Fatalf("len = %d, want %d (every category × channel)", len(got), want)
	}
	for _, p := range got {
		switch {
		case p.Category == domain.CategoryOrder && p.Channel == domain.ChannelPush:
			if p.IsEnabled || p.IsDefault {
				t.Errorf("stored pair = %+v, want disabled and not default", p)
			}
		default:
			if !p.IsDefault {
				t.Errorf("pair %+v came back as an explicit choice", p)
			}
			if p.IsEnabled != domain.DefaultPreference(p.Category, p.Channel) {
				t.Errorf("pair %+v does not match the domain default", p)
			}
		}
	}
}

// Setting a pair back to its default deletes the row rather than storing the default
// again: that is what keeps the table sparse and the defaults free to change.
func TestSplitPreferences_DefaultsBecomeDeletes(t *testing.T) {
	// in-app/order is on by default; push/promotion is off by default.
	want := []domain.Preference{
		{Category: domain.CategoryOrder, Channel: domain.ChannelInApp, IsEnabled: true},     // == default
		{Category: domain.CategoryPromotion, Channel: domain.ChannelPush, IsEnabled: false}, // == default
		{Category: domain.CategoryOrder, Channel: domain.ChannelEmail, IsEnabled: false},    // deviation
	}
	store, remove := domain.SplitPreferences(want)

	if len(store) != 1 || store[0].Channel != domain.ChannelEmail {
		t.Fatalf("store = %+v, want only the deviation", store)
	}
	if len(remove) != 2 {
		t.Fatalf("remove = %+v, want both defaults", remove)
	}
}

// SMS is off everywhere by default: it costs money per message and interrupts hardest, so
// it is opt-in.
func TestDefaultPreference_SMSIsOptIn(t *testing.T) {
	for _, c := range domain.Categories {
		if domain.DefaultPreference(c, domain.ChannelSMS) {
			t.Errorf("category %q has SMS on by default", c)
		}
		if !domain.DefaultPreference(c, domain.ChannelInApp) {
			t.Errorf("category %q has in-app off by default", c)
		}
	}
}

// An unknown pair reads as "off" rather than panicking on a missing map entry, so a value
// that slipped past validation cannot turn into an unwanted send.
func TestDefaultPreference_UnknownPairIsOff(t *testing.T) {
	if domain.DefaultPreference(domain.Category("nope"), domain.ChannelPush) {
		t.Fatal("an unknown category defaulted to enabled")
	}
}

func TestNewNotification(t *testing.T) {
	tests := []struct {
		name   string
		params domain.NewNotificationParams
		want   error
	}{
		{
			name: "valid",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Kind:      domain.KindOrderDelivered,
				Payload:   map[string]any{"order_id": "ord_x"},
			},
		},
		{
			name: "account required",
			params: domain.NewNotificationParams{
				Kind: domain.KindOrderDelivered,
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "kind required",
			params: domain.NewNotificationParams{
				AccountID: 42,
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "kind must be known",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Kind:      domain.Kind("gossip"),
			},
			want: domain.ErrNotificationInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewNotification(tt.params)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want != nil {
				return
			}
			if got.AccountID != tt.params.AccountID {
				t.Errorf("AccountID = %d, want %d", got.AccountID, tt.params.AccountID)
			}
			// The category is not sent; it is what the kind files under, which is what stops a
			// caller filing an order fact under 'promotion' and having it silenced.
			if got.Category != domain.CategoryOrder {
				t.Errorf("Category = %q, want it derived from the kind", got.Category)
			}
			if got.CreatedAt.IsZero() {
				t.Error("CreatedAt is zero; the constructor stamps it")
			}
			if got.ReadAt != nil {
				t.Error("ReadAt should be nil on a fresh notification")
			}
		})
	}
}

// A scheduled notification is not yet delivered, so it must not read as unread now.
func TestNewNotificationScheduled(t *testing.T) {
	at := time.Now().Add(time.Hour)
	got, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID:   7,
		Kind:        domain.KindListingApproved,
		ScheduledAt: &at,
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	if got.ScheduledAt == nil || !got.ScheduledAt.Equal(at) {
		t.Fatalf("ScheduledAt = %v, want %v", got.ScheduledAt, at)
	}
}

// Every kind resolves to a category and a link builder. A kind added to the vocabulary without
// either is a feed row with no preference deciding it and nowhere to go — which the constructor
// would happily accept, because it only checks the kind is known.
func TestKindSpecs_AreComplete(t *testing.T) {
	if len(domain.Kinds) == 0 {
		t.Fatal("no kinds; the vocabulary is derived from the spec map and came out empty")
	}
	for _, kind := range domain.Kinds {
		spec, ok := domain.SpecOf(kind)
		if !ok {
			t.Fatalf("kind %q is listed but has no spec", kind)
		}
		if !slices.Contains(domain.Categories, spec.Category) {
			t.Errorf("kind %q files under unknown category %q", kind, spec.Category)
		}
		if spec.Href == nil {
			t.Errorf("kind %q has no link builder", kind)
		}
	}
}

// A link builder reads the payload and must answer empty rather than a path to nowhere when the
// key it names is missing — a row with no link is readable, a link to /account/orders/ is a 404.
func TestKindSpecs_HrefTolerantOfAMissingKey(t *testing.T) {
	for _, kind := range domain.Kinds {
		spec, _ := domain.SpecOf(kind)
		href := spec.Href(map[string]any{})
		if strings.HasSuffix(href, "/") {
			t.Errorf("kind %q builds a dangling link %q from an empty payload", kind, href)
		}
	}
}

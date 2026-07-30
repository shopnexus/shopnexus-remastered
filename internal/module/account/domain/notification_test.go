package domain_test

import (
	"testing"

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

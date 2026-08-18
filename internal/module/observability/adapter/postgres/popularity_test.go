package postgres

import "testing"

// counterOf mirrors catalogapi.Interaction* by hand — this test is the one thing that would
// catch a vocabulary the two files drifted apart on.
func TestCounterOf(t *testing.T) {
	for _, tc := range []struct {
		interactionType string
		want            string
	}{
		{"view", "view"},
		{"click-from-search", "click"},
		{"click-from-recommended", "click"},
		{"click-from-category", "click"},
		{"not-interested", "dismiss"},
		{"hidden", "dismiss"},
		{"purchase", "purchase"},
		{"something-nobody-weighed", ""},
	} {
		if got := counterOf(tc.interactionType); got != tc.want {
			t.Errorf("counterOf(%q) = %q, want %q", tc.interactionType, got, tc.want)
		}
	}
}

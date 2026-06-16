package patch_test

import (
	"testing"

	"shopnexus-server/internal/shared/patch"
)

func TestZeroValueIsUnset(t *testing.T) {
	var o patch.Optional[string]
	if o.Set {
		t.Fatal("zero-value Optional must be unset")
	}
}

func TestSetCarriesValue(t *testing.T) {
	o := patch.Set("hello")
	if !o.Set {
		t.Fatal("Set() must mark the field present")
	}
	if o.Value != "hello" {
		t.Fatalf("Value = %q, want %q", o.Value, "hello")
	}
}

func TestSetCanCarryNullableValue(t *testing.T) {
	type nullable struct{ Valid bool }
	o := patch.Set(nullable{Valid: false})
	if !o.Set {
		t.Fatal("Set() of a NULL-carrying value is still present")
	}
	if o.Value.Valid {
		t.Fatal("value should round-trip unchanged")
	}
}

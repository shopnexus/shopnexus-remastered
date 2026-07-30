package patch_test

import (
	"encoding/json"
	"testing"

	"shopnexus/internal/shared/patch"
)

// The whole point of the type: three states, and a PATCH body needs all three. Absent
// leaves a field alone, null clears it, a value replaces it.
func TestUnmarshal_ThreeStates(t *testing.T) {
	type body struct {
		Email patch.Field[string] `json:"email"`
	}

	for _, tc := range []struct {
		name            string
		raw             string
		present, isNull bool
		value           string
	}{
		{name: "absent", raw: `{}`},
		{name: "null", raw: `{"email":null}`, present: true, isNull: true},
		{name: "value", raw: `{"email":"a@b.com"}`, present: true, value: "a@b.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b body
			if err := json.Unmarshal([]byte(tc.raw), &b); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if b.Email.Present() != tc.present {
				t.Errorf("Present() = %v, want %v", b.Email.Present(), tc.present)
			}
			if b.Email.Null() != tc.isNull {
				t.Errorf("Null() = %v, want %v", b.Email.Null(), tc.isNull)
			}
			v, ok := b.Email.Get()
			if ok != (tc.value != "") || v != tc.value {
				t.Errorf("Get() = %q, %v; want %q", v, ok, tc.value)
			}
		})
	}
}

// Get is for "did the client send me something to store": both absent and null answer no,
// which is what lets a handler apply a value without asking about the other two states.
func TestGet_NullAndAbsentBothSayNo(t *testing.T) {
	if _, ok := patch.Clear[string]().Get(); ok {
		t.Error("Get() on an explicit null reported a value")
	}
	if _, ok := (patch.Field[string]{}).Get(); ok {
		t.Error("Get() on an absent field reported a value")
	}
	if v, ok := patch.Of("x").Get(); !ok || v != "x" {
		t.Errorf("Get() = %q, %v; want x, true", v, ok)
	}
}

func TestUnmarshal_WrongTypeFails(t *testing.T) {
	var f patch.Field[string]
	if err := json.Unmarshal([]byte(`42`), &f); err == nil {
		t.Fatal("expected an error for a number in a string field")
	}
}

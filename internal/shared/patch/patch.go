// Package patch carries the three states a PATCH field can be in.
//
// A pointer only has two: nil or a value, which conflates "the client did not
// mention this field" with "the client asked to clear it". Every PATCH body in
// this API needs both — PATCH /me removes an identifier by sending null, and
// omitting the same key must leave it alone — so the distinction is a type
// rather than a convention.
package patch

import "encoding/json"

// Field is an optional, nullable request field. The zero value is "absent".
type Field[T any] struct {
	present bool
	value   *T
}

// Present reports whether the key appeared in the body at all.
func (f Field[T]) Present() bool { return f.present }

// Null reports whether the client explicitly sent null — the request to clear.
func (f Field[T]) Null() bool { return f.present && f.value == nil }

// Get returns the value the client sent. ok is false when the field is absent or
// null, so a caller that only cares about a new value can ignore both at once.
func (f Field[T]) Get() (value T, ok bool) {
	if f.value == nil {
		var zero T
		return zero, false
	}
	return *f.value, true
}

// Ptr returns the value as a pointer: nil for both absent and null. Handy for a
// column that is already nullable, where the two mean the same thing.
func (f Field[T]) Ptr() *T { return f.value }

// Of builds a present field holding v — for tests and for a service that
// composes a patch itself.
func Of[T any](v T) Field[T] { return Field[T]{present: true, value: &v} }

// Clear builds a present field holding null.
func Clear[T any]() Field[T] { return Field[T]{present: true} }

func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.present = true
	if string(b) == "null" {
		f.value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	f.value = &v
	return nil
}

// MarshalJSON exists so a Field survives a round trip through a log or a test
// fixture; an absent field renders as null, since JSON has no "absent" value
// outside an object key.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*f.value)
}

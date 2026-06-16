// Package patch carries partial-update fields for generated repositories.
package patch

// Optional is a tri-state update field. The zero value is "unset" (leave the
// column untouched). Set marks the field present; Value carries the new value,
// which for a nullable column is itself a value-or-NULL type — so the two layers
// express unset / set-value / set-NULL without a separate flag.
type Optional[T any] struct {
	Set   bool
	Value T
}

// Set returns a present Optional carrying v.
func Set[T any](v T) Optional[T] { return Optional[T]{Set: true, Value: v} }

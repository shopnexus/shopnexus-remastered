package postgres

import (
	"testing"

	"shopnexus/internal/module/catalog/port"
)

// A kind with no fragment compiles, reaches searchStatement and is dropped in silence, so the
// signal the model paid for ranks nothing. The whitelist has to be exactly the set.
func TestPredicateSQL_CoversEveryKind(t *testing.T) {
	for _, kind := range port.PredicateKinds {
		if _, ok := predicateSQL[kind]; !ok {
			t.Errorf("kind %q has no predicateSQL fragment", kind)
		}
	}
	if len(predicateSQL) != len(port.PredicateKinds) {
		t.Errorf("predicateSQL holds %d fragments over %d kinds — one of them is unreachable",
			len(predicateSQL), len(port.PredicateKinds))
	}
}

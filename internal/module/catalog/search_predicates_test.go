package catalog_test

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// domain cannot import port, so the predicate kinds are declared twice — the same duplication
// CLAUDE.md already accepts for api/domain constant pairs. This is the check that makes it safe: a
// rename on one side that misses the other fails here instead of silently routing a compiled
// predicate to a kind the adapter's whitelist does not recognize.
//
// The count is asserted too, against port.PredicateKinds — the set's one authoritative listing.
// Without it a kind added to both sides but not to this table is a compiled signal nobody checks,
// and TestPredicateSQL_CoversEveryKind is the same assertion for the adapter's SQL.
func TestPredicateKinds_DomainAgreesWithPort(t *testing.T) {
	pairs := []struct{ domainKind, portKind string }{
		{domain.PredicateCategory, port.PredicateCategory},
		{domain.PredicateTag, port.PredicateTag},
		{domain.PredicateMinPrice, port.PredicateMinPrice},
		{domain.PredicateMaxPrice, port.PredicateMaxPrice},
		{domain.PredicateCondition, port.PredicateCondition},
	}
	if len(pairs) != len(port.PredicateKinds) {
		t.Fatalf("%d pairs over %d kinds — a kind reached neither this table nor its twin",
			len(pairs), len(port.PredicateKinds))
	}
	for _, p := range pairs {
		if p.domainKind != p.portKind {
			t.Errorf("domain kind %q != port kind %q", p.domainKind, p.portKind)
		}
	}
}

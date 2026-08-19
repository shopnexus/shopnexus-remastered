package catalog_test

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// domain cannot import port, so the seven predicate kinds are declared twice — the same
// duplication CLAUDE.md already accepts for api/domain constant pairs. This is the check that
// makes it safe: a rename on one side that misses the other fails here instead of silently
// routing a compiled predicate to a kind the adapter's whitelist does not recognize.
func TestPredicateKinds_DomainAgreesWithPort(t *testing.T) {
	pairs := []struct{ domainKind, portKind string }{
		{domain.PredicateCategory, port.PredicateCategory},
		{domain.PredicateTag, port.PredicateTag},
		{domain.PredicateMinPrice, port.PredicateMinPrice},
		{domain.PredicateMaxPrice, port.PredicateMaxPrice},
		{domain.PredicateCondition, port.PredicateCondition},
		{domain.PredicateProvince, port.PredicateProvince},
		{domain.PredicateWard, port.PredicateWard},
	}
	for _, p := range pairs {
		if p.domainKind != p.portKind {
			t.Errorf("domain kind %q != port kind %q", p.domainKind, p.portKind)
		}
	}
}

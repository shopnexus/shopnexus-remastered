package postgres

import (
	"testing"

	"shopnexus/internal/module/catalog/port"
)

// pgx has no codec for the vector OID, so the adapter writes and reads pgvector's text format
// itself. A round trip is the whole contract.
func TestVectorLiteralRoundTrip(t *testing.T) {
	in := port.Vector{0.5, -1, 0, 1234.25}
	got, err := parseVector(vectorLiteral(in))
	if err != nil {
		t.Fatalf("parseVector: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("element %d = %v, want %v", i, got[i], in[i])
		}
	}
	if literal := vectorLiteral(port.Vector{1, 2}); literal != "[1,2]" {
		t.Errorf("literal = %q, want [1,2]", literal)
	}
	// An empty literal is how a NULL column reads once cast to text by a caller that did not
	// filter it out; it must not become a zero-length probe that ranks everything.
	empty, err := parseVector("[]")
	if err != nil || empty != nil {
		t.Errorf("parseVector(\"[]\") = %v, %v; want nil, nil", empty, err)
	}
}

// The centroid of one probe is that probe; of several, their average — which is what "near
// all of these" means in a cosine space.
func TestCentroid(t *testing.T) {
	single := port.Vector{1, 2}
	if got := centroid([]port.Vector{single}); got[0] != 1 || got[1] != 2 {
		t.Fatalf("centroid of one = %v", got)
	}
	got := centroid([]port.Vector{{0, 4}, {2, 0}})
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("centroid = %v, want [1 2]", got)
	}
}

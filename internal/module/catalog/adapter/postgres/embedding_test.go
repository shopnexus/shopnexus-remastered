package postgres

import (
	"strconv"
	"strings"
	"testing"
)

// The literal is where the model's convention meets pgvector's, and they disagree about where
// counting starts. An off-by-one here is not an error — it is every vector silently indexed
// under the wrong term.
func TestSparseLiteralShiftsToOneBasedIndices(t *testing.T) {
	got, err := sparseLiteral(map[uint32]float32{0: 0.5, 3: 0.25}, 16)
	if err != nil {
		t.Fatalf("sparseLiteral: %v", err)
	}
	if want := "{1:0.5,4:0.25}/16"; got != want {
		t.Errorf("literal = %q, want %q", got, want)
	}
}

// Ascending index order and the trailing dimension are what pgvector's parser expects.
func TestSparseLiteralIsOrderedAndDimensioned(t *testing.T) {
	got, err := sparseLiteral(map[uint32]float32{9: 1, 2: 1, 5: 1}, 100)
	if err != nil {
		t.Fatalf("sparseLiteral: %v", err)
	}
	if want := "{3:1,6:1,10:1}/100"; got != want {
		t.Errorf("literal = %q, want %q", got, want)
	}
}

// A stored zero is a non-zero that says nothing, and it counts against the index's cap.
func TestSparseLiteralDropsZeroWeights(t *testing.T) {
	got, err := sparseLiteral(map[uint32]float32{0: 0, 1: 2}, 16)
	if err != nil {
		t.Fatalf("sparseLiteral: %v", err)
	}
	if want := "{2:2}/16"; got != want {
		t.Errorf("literal = %q, want %q", got, want)
	}
}

// HNSW refuses a sparsevec with more than a thousand non-zeros — at the INSERT, so an overlong
// document is a failed write rather than a poor result. The heaviest terms are kept.
func TestSparseLiteralKeepsTheHeaviestTermsWithinTheCap(t *testing.T) {
	weights := map[uint32]float32{}
	for i := uint32(0); i < 1500; i++ {
		weights[i] = float32(i) // the last 1000 indices are the heaviest
	}
	got, err := sparseLiteral(weights, 250048)
	if err != nil {
		t.Fatalf("sparseLiteral: %v", err)
	}
	terms := strings.Split(strings.TrimSuffix(strings.TrimPrefix(got, "{"), "}/250048"), ",")
	if len(terms) != maxSparseNonZero {
		t.Fatalf("kept %d terms, want the cap of %d", len(terms), maxSparseNonZero)
	}
	// Index 500 (weight 500) is the lightest survivor, one-based 501; 499 must have been cut.
	first := strings.SplitN(terms[0], ":", 2)[0]
	if n, _ := strconv.Atoi(first); n != 501 {
		t.Errorf("lightest kept index = %s, want 501 — the cut took the smallest weights", first)
	}
}

// An index past the column's width is a row Postgres would reject with nothing to point at,
// so it is caught here where the number can be named.
func TestSparseLiteralRefusesAnIndexOutsideTheColumn(t *testing.T) {
	if _, err := sparseLiteral(map[uint32]float32{16: 1}, 16); err == nil {
		t.Error("sparseLiteral accepted an index at the column width, want it refused")
	}
}

func TestDenseLiteral(t *testing.T) {
	if got, want := denseLiteral([]float32{1, -0.5, 0}), "[1,-0.5,0]"; got != want {
		t.Errorf("denseLiteral = %q, want %q", got, want)
	}
	if got, want := denseLiteral(nil), "[]"; got != want {
		t.Errorf("denseLiteral(nil) = %q, want %q", got, want)
	}
}

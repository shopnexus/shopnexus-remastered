package port

import (
	"context"
	"time"
)

// Kind is which dictionary a queued row belongs to. Three tables carry an
// `embedding_stale_at` and three carry a vector beside it, and the pass over each is the same
// shape — so the kind is a value rather than three copies of the code.
type Kind string

const (
	KindListing  Kind = "listing"
	KindCategory Kind = "category"
	KindTag      Kind = "tag"
)

// Kinds is every queue the indexer drains, in the order it drains them. Categories and tags
// first because they are few and a listing's suggestions rank against them: a fresh
// deployment answers useful category hints within one pass instead of after the listings.
var Kinds = []Kind{KindCategory, KindTag, KindListing}

// Stale is one row waiting to be embedded.
type Stale struct {
	Kind Kind
	// ID for a listing or a category; Slug for a tag, whose key is its natural one. Exactly
	// one is set, decided by Kind.
	ID   int64
	Slug string
	// Text is what the model reads, composed by the adapter from the columns that describe
	// the row — see the composition rules in the catalog indexer.
	Text string
	// StaleAt is the mark this pass is clearing. Written back as a guard rather than
	// discarded: a row edited while the model was working must stay queued, and comparing
	// against the value that was read is the only way to tell that apart from the pass's own
	// work. Same idiom as a version-guarded aggregate write.
	StaleAt time.Time
}

// Embedded is one finished vector, paired back to the row it came from.
//
// Sparse indices are the model's own, counted from zero; the adapter shifts them into
// pgvector's one-based literal, because that is a storage convention and not the model's.
type Embedded struct {
	Stale
	Dense  []float32
	Sparse map[uint32]float32
}

// Embeddings is the indexer's whole view of catalog: read what is stale, write what came
// back. Deliberately not part of Repository — the indexer is a separate process with no
// business loading a listing aggregate, and Repository has no business growing a queue drain.
type Embeddings interface {
	// ListStale reads the oldest marks first, so a row that has been waiting does not starve
	// behind a listing somebody edits every minute.
	ListStale(ctx context.Context, kind Kind, limit int) ([]Stale, error)
	// Save writes each vector and clears the mark it was computed from, in one transaction:
	// a vector that landed is always a row that left the queue, and a row that stayed queued
	// never has a half-written vector beside it.
	//
	// A row re-marked since ListStale keeps its new mark and is picked up again — its vector
	// is still written, because a slightly stale vector beats none while it waits.
	Save(ctx context.Context, done []Embedded) error
}

// Package embedding is the seam that turns text into the two vectors this platform searches
// on: a dense one for meaning and a sparse one for the words actually used.
//
// Separate from `provider/llm`, whose Embed is dense-only and speaks to an OpenAI-shaped
// gateway. Hybrid retrieval needs both halves out of one pass over the text — a model that
// produces them together is a different kind of dependency, not a different vendor of the
// same one.
package embedding

import (
	"context"
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Vector is one text's embedding.
//
// Sparse indices are the model's own, counted from zero, exactly as it reports them. Turning
// them into pgvector's one-based `sparsevec` literal is the adapter's job: that is storage's
// convention, and a provider that pre-applied it would be encoding a database's rules.
type Vector struct {
	Dense  []float32
	Sparse map[uint32]float32
}

// Client is the model. One method, because one text in and one vector out is the whole
// capability — batching is a parameter of it and not a second operation.
type Client interface {
	// Name identifies the model behind this client, for the log line that says which one
	// wrote a vector. kebab-case.
	Name() string
	// Embed returns one vector per text, in the same order. A partial answer is an error:
	// the caller pairs results with rows positionally, and a short slice would silently
	// attach one row's vector to another.
	Embed(ctx context.Context, texts []string) ([]Vector, error)
}

// ErrUnexpectedDimensions is a model answering in a size this deployment's schema cannot
// hold. Its own error because it is the one failure that is a deployment mistake rather than
// a bad moment: `catalog.listing_embedding.dense` is `vector(1024)`, so a 768-dimension model
// does not degrade — every row fails, for ever, until somebody changes one of the two.
var ErrUnexpectedDimensions = errx.NewError(http.StatusInternalServerError, "embedding_dimensions_mismatch",
	"the embedding model answered in a size this deployment does not store")

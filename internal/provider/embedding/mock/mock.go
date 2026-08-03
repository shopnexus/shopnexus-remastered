// Package mock is the embedding seam without the model: a deterministic hash of the words in
// the text, in the shape the real one answers.
//
// It exists so the indexer, the queue drain and the two vector columns can all be exercised
// on a laptop — the real model is a multi-gigabyte download that wants a GPU, and a
// development stack that cannot run without one is a stack nobody runs. The vectors are
// meaningless as *meaning*: two texts sharing words come out near each other, which is enough
// to prove the pipeline and nothing at all about retrieval quality.
package mock

import (
	"context"
	"hash/fnv"
	"math"
	"strings"

	"shopnexus/internal/provider/embedding"
)

// Name is the EMBEDDING_PROVIDER value that selects this client.
const Name = "mock"

// vocabulary is the sparse index space, matching the real tokenizer's so a mock-written row
// and a model-written one live in the same column without one of them being out of range.
const vocabulary = 250002

type Client struct{ dimensions int }

func New(dimensions int) *Client { return &Client{dimensions: dimensions} }

var _ embedding.Client = (*Client)(nil)

func (c *Client) Name() string { return Name }

func (c *Client) Embed(ctx context.Context, texts []string) ([]embedding.Vector, error) {
	out := make([]embedding.Vector, len(texts))
	for i, text := range texts {
		out[i] = c.one(text)
	}
	return out, nil
}

func (c *Client) one(text string) embedding.Vector {
	dense := make([]float32, c.dimensions)
	sparse := map[uint32]float32{}
	for _, word := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		sum := h.Sum32()
		// The word contributes to one dense slot and one sparse index, so a shared word moves
		// two texts together in both halves — the property the pipeline is checked against.
		dense[sum%uint32(c.dimensions)] += 1
		sparse[sum%vocabulary] += 1
	}
	return embedding.Vector{Dense: unit(dense), Sparse: sparse}
}

// unit scales the vector to length 1, because the dense index is cosine and an unnormalised
// vector would rank by how many words a text has.
func unit(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		// An empty text still has to be a storable vector: the column is not nullable once
		// written, and a zero vector is the honest answer for "nothing to go on".
		return v
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
	return v
}

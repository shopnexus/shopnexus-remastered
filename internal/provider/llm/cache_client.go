package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"shopnexus-server/internal/infras/cache"
)

// Compile-time interface check.
var _ Client = (*CachingClient)(nil)

const embedCachePrefix = "llm:embed:"

// CachingClient wraps an llm.Client and caches Embed results keyed by text
// content. Embeddings are deterministic for a given model+text, so repeated
// queries (the common case for product search) skip the embedding round-trip.
// Generation methods pass through — their output is non-deterministic.
type CachingClient struct {
	inner Client
	cache cache.Client
	ttl   time.Duration // 0 = cache forever
}

// NewCachingClient wraps inner with an embedding cache.
func NewCachingClient(inner Client, c cache.Client, ttl time.Duration) *CachingClient {
	return &CachingClient{inner: inner, cache: c, ttl: ttl}
}

func embedKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return embedCachePrefix + hex.EncodeToString(sum[:])
}

// Embed returns cached embeddings where available and embeds only the misses in
// a single batch call, preserving input order.
func (c *CachingClient) Embed(ctx context.Context, texts []string) ([]EmbedResult, error) {
	results := make([]EmbedResult, len(texts))
	missTexts := make([]string, 0, len(texts))
	missIdx := make([]int, 0, len(texts))

	for i, t := range texts {
		var hit EmbedResult
		if err := c.cache.Get(ctx, embedKey(t), &hit); err == nil {
			results[i] = hit
			continue
		}
		missTexts = append(missTexts, t)
		missIdx = append(missIdx, i)
	}

	if len(missTexts) == 0 {
		return results, nil
	}

	embedded, err := c.inner.Embed(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	for j, r := range embedded {
		results[missIdx[j]] = r
		// Cache-write failure must not fail the request.
		_ = c.cache.Set(ctx, embedKey(missTexts[j]), r, c.ttl)
	}

	return results, nil
}

func (c *CachingClient) GenerateText(ctx context.Context, params GenerateTextParams) (string, error) {
	return c.inner.GenerateText(ctx, params)
}

func (c *CachingClient) Chat(ctx context.Context, params ChatParams) (ChatMessage, error) {
	return c.inner.Chat(ctx, params)
}

func (c *CachingClient) GenerateStructured(ctx context.Context, params GenerateStructuredParams) (json.RawMessage, error) {
	return c.inner.GenerateStructured(ctx, params)
}

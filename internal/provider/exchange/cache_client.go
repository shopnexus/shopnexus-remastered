package exchange

import (
	"context"
	"sort"
	"strings"
	"time"

	"shopnexus-server/internal/infras/cache"
)

// Compile-time interface check.
var _ Client = (*CachingClient)(nil)

const cacheKeyPrefix = "exchange:rates:"

// CachingClient wraps an exchange.Client with a read-through cache. Rates for a
// given base+targets are deterministic over the TTL window, so repeated lookups
// (every price conversion) skip the upstream HTTP round-trip. On a miss it fetches
// once and caches; the TTL bounds staleness.
type CachingClient struct {
	inner Client
	cache cache.Client
	ttl   time.Duration
}

// NewCachingClient wraps inner with a rate cache. ttl <= 0 caches forever.
func NewCachingClient(inner Client, c cache.Client, ttl time.Duration) *CachingClient {
	return &CachingClient{inner: inner, cache: c, ttl: ttl}
}

// cacheKey sorts targets so call order doesn't fragment the cache.
func cacheKey(base string, targets []string) string {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)
	return cacheKeyPrefix + base + ":" + strings.Join(sorted, ",")
}

// FetchLatest returns the cached snapshot when present, otherwise fetches from
// the upstream provider and caches the result.
func (c *CachingClient) FetchLatest(ctx context.Context, base string, targets []string) (Snapshot, error) {
	key := cacheKey(base, targets)

	var cached Snapshot
	if err := c.cache.Get(ctx, key, &cached); err == nil {
		return cached, nil
	}

	fresh, err := c.inner.FetchLatest(ctx, base, targets)
	if err != nil {
		return Snapshot{}, err
	}

	// Cache-write failure must not fail the request.
	_ = c.cache.Set(ctx, key, fresh, c.ttl)
	return fresh, nil
}

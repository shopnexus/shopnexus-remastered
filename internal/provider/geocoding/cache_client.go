package geocoding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"shopnexus-server/internal/infras/cache"
)

// Compile-time interface check.
var _ Client = (*CachingClient)(nil)

const forwardCachePrefix = "geocoding:forward:"

// CachingClient wraps a geocoding.Client with a read-through cache on
// ForwardGeocode. Address -> coordinates/country mappings rarely change, so the
// cache is the primary defence against Nominatim's 1 req/sec public rate limit.
type CachingClient struct {
	inner Client
	cache cache.Client
	ttl   time.Duration // 0 = cache forever
}

// NewCachingClient wraps inner with a forward-geocode cache. ttl <= 0 caches forever.
func NewCachingClient(inner Client, c cache.Client, ttl time.Duration) *CachingClient {
	return &CachingClient{inner: inner, cache: c, ttl: ttl}
}

func forwardKey(address string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address))))
	return forwardCachePrefix + hex.EncodeToString(sum[:])[:16]
}

// ForwardGeocode returns the cached result when present, otherwise resolves via
// the inner client and caches the result.
func (c *CachingClient) ForwardGeocode(ctx context.Context, address string) (Result, error) {
	key := forwardKey(address)

	var cached Result
	if err := c.cache.Get(ctx, key, &cached); err == nil {
		return cached, nil
	}

	fresh, err := c.inner.ForwardGeocode(ctx, address)
	if err != nil {
		return Result{}, err
	}

	// Cache-write failure must not fail the request.
	_ = c.cache.Set(ctx, key, fresh, c.ttl)
	return fresh, nil
}

// ReverseGeocode passes through — coordinate lookups are not currently cached.
func (c *CachingClient) ReverseGeocode(ctx context.Context, lat, lng float64) (Result, error) {
	return c.inner.ReverseGeocode(ctx, lat, lng)
}

// Search passes through — list results are not currently cached.
func (c *CachingClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	return c.inner.Search(ctx, query, limit)
}

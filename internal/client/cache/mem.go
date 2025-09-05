package cache

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// cacheItem represents a cached value with expiration time
type cacheItem struct {
	value      string
	expiration time.Time
	hasExpiry  bool
}

// isExpired checks if the item has expired
func (item *cacheItem) isExpired() bool {
	if !item.hasExpiry {
		return false
	}
	return time.Now().After(item.expiration)
}

// InMemoryCache implements the Client interface with an in-memory store
type InMemoryCache struct {
	store   map[string]*cacheItem
	mutex   sync.RWMutex
	cleanup *time.Ticker
	stop    chan struct{}
}

// NewInMemoryClient creates a new in-memory cache with optional cleanup interval
func NewInMemoryClient() *InMemoryCache {
	cache := &InMemoryCache{
		store: make(map[string]*cacheItem),
		stop:  make(chan struct{}),
	}

	// Start cleanup goroutine
	cache.cleanup = time.NewTicker(30 * time.Second)
	go cache.cleanupExpired()

	return cache
}

// cleanupExpired removes expired items from the cache
func (c *InMemoryCache) cleanupExpired() {
	for {
		select {
		case <-c.cleanup.C:
			c.mutex.Lock()
			for key, item := range c.store {
				if item.isExpired() {
					delete(c.store, key)
				}
			}
			c.mutex.Unlock()
		case <-c.stop:
			return
		}
	}
}

// Get retrieves a value from the cache
func (c *InMemoryCache) Get(ctx context.Context, key string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, exists := c.store[key]
	if !exists {
		return "", fmt.Errorf("key not found: %s", key)
	}

	if item.isExpired() {
		// Remove expired item
		delete(c.store, key)
		return "", fmt.Errorf("key expired: %s", key)
	}

	return item.value, nil
}

// Set stores a value in the cache with optional expiration
func (c *InMemoryCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	item := &cacheItem{
		value: value,
	}

	if expiration > 0 {
		item.expiration = time.Now().Add(expiration)
		item.hasExpiry = true
	}

	c.store[key] = item
	return nil
}

// Delete removes a key from the cache
func (c *InMemoryCache) Delete(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.store, key)
	return nil
}

// Exists checks if a key exists and is not expired
func (c *InMemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, exists := c.store[key]
	if !exists {
		return false, nil
	}

	if item.isExpired() {
		// Remove expired item
		delete(c.store, key)
		return false, nil
	}

	return true, nil
}

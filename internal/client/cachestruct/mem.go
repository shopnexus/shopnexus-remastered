package cachestruct

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
)

// cacheItem holds the cached value and its expiration time
type cacheItem struct {
	value      any
	expiration time.Time
}

// isExpired checks if the item has expired
func (item *cacheItem) isExpired() bool {
	return !item.expiration.IsZero() && time.Now().After(item.expiration)
}

// InMemoryCache implements the Client interface using a simple map
type InMemoryCache struct {
	mu    sync.RWMutex
	items map[string]*cacheItem

	// Optional: cleanup ticker for expired items
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// NewInMemoryClient creates a new in-memory cache instance
func NewInMemoryClient() *InMemoryCache {
	cache := &InMemoryCache{
		items:       make(map[string]*cacheItem),
		stopCleanup: make(chan struct{}),
	}

	// Start background cleanup routine (runs every 5 minutes)
	cache.cleanupTicker = time.NewTicker(5 * time.Minute)
	go cache.cleanupExpired()

	return cache
}

// Get retrieves a value from cache and copies it to dest
func (c *InMemoryCache) Get(ctx context.Context, key string, dest any) error {
	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.RLock()
	item, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		return errors.New("key not found")
	}

	// Check if item is expired
	if item.isExpired() {
		// Remove expired item
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return errors.New("key not found")
	}

	// Copy value to destination using reflection
	return c.copyValue(item.value, dest)
}

// Set stores a value in cache with expiration
func (c *InMemoryCache) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	item := &cacheItem{
		value: value,
	}

	// Set expiration time if duration is positive
	if expiration > 0 {
		item.expiration = time.Now().Add(expiration)
	}

	c.mu.Lock()
	c.items[key] = item
	c.mu.Unlock()

	return nil
}

// Delete removes a key from cache
func (c *InMemoryCache) Delete(ctx context.Context, key string) error {
	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()

	return nil
}

// Exists checks if a key exists and is not expired
func (c *InMemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	c.mu.RLock()
	item, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		return false, nil
	}

	// Check if item is expired
	if item.isExpired() {
		// Remove expired item
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return false, nil
	}

	return true, nil
}

// cleanupExpired runs in background to remove expired items
func (c *InMemoryCache) cleanupExpired() {
	for {
		select {
		case <-c.cleanupTicker.C:
			c.removeExpiredItems()
		case <-c.stopCleanup:
			return
		}
	}
}

// removeExpiredItems removes all expired items from cache
func (c *InMemoryCache) removeExpiredItems() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, item := range c.items {
		if item.isExpired() {
			delete(c.items, key)
		}
	}
}

// copyValue copies source value to destination using reflection
func (c *InMemoryCache) copyValue(src, dest any) error {
	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Ptr {
		return errors.New("destination must be a pointer")
	}

	destVal = destVal.Elem()
	if !destVal.CanSet() {
		return errors.New("destination cannot be set")
	}

	srcVal := reflect.ValueOf(src)
	if !srcVal.Type().AssignableTo(destVal.Type()) {
		return errors.New("source type cannot be assigned to destination type")
	}

	destVal.Set(srcVal)
	return nil
}

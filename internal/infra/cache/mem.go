package cache

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
)

type cacheItem struct {
	value      any
	expiration time.Time
}

func (item *cacheItem) isExpired() bool {
	return !item.expiration.IsZero() && time.Now().After(item.expiration)
}

// InMemoryCache is the Client a test uses: a map plus a janitor, no server to run.
type InMemoryCache struct {
	mu    sync.RWMutex
	items map[string]*cacheItem

	// stop closes the janitor that drops expired entries
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
	closeOnce     sync.Once
}

func NewInMemoryClient() *InMemoryCache {
	cache := new(InMemoryCache)
	cache.items = make(map[string]*cacheItem)
	cache.stopCleanup = make(chan struct{})

	cache.cleanupTicker = time.NewTicker(5 * time.Minute)
	go cache.cleanupExpired()

	return cache
}

func (c *InMemoryCache) Get(ctx context.Context, key string, dest any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	item, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		return ErrCacheMiss
	}

	if item.isExpired() {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return ErrCacheMiss
	}

	return c.copyValue(item.value, dest)
}

func (c *InMemoryCache) GetDel(ctx context.Context, key string, dest any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.items[key]
	if !exists {
		return ErrCacheMiss
	}

	if item.isExpired() {
		delete(c.items, key)
		return ErrCacheMiss
	}

	// One critical section covering both halves — the point of the method.
	if err := c.copyValue(item.value, dest); err != nil {
		return err
	}
	delete(c.items, key)
	return nil
}

func (c *InMemoryCache) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	item := &cacheItem{
		value: value,
	}

	if expiration > 0 {
		item.expiration = time.Now().Add(expiration)
	}

	c.mu.Lock()
	c.items[key] = item
	c.mu.Unlock()

	return nil
}

func (c *InMemoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()

	return nil
}

// Exists checks if a key exists and is not expired.
func (c *InMemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	c.mu.RLock()
	item, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		return false, nil
	}

	if item.isExpired() {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return false, nil
	}

	return true, nil
}

// cleanupExpired runs in background to remove expired items.
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

// removeExpiredItems removes all expired items from cache.
func (c *InMemoryCache) removeExpiredItems() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, item := range c.items {
		if item.isExpired() {
			delete(c.items, key)
		}
	}
}

// copyValue copies source value to destination using reflection.
func (c *InMemoryCache) copyValue(src, dest any) error {
	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Pointer {
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

// Ping always succeeds for the in-memory cache.
func (c *InMemoryCache) Ping() error {
	return nil
}

// Close stops the background cleanup goroutine. Safe to call more than once.
func (c *InMemoryCache) Close() error {
	c.closeOnce.Do(func() {
		c.cleanupTicker.Stop()
		close(c.stopCleanup)
	})
	return nil
}

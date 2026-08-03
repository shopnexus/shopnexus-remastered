package cache

import (
	"context"
	"errors"
	"time"
)

// ErrCacheMiss is returned by Get when the key is absent.
var ErrCacheMiss = errors.New("cache: key not found")

// Client is a key/value cache over JSON-encoded values. Get decodes into dest, so a caller works
// in its own types rather than in bytes.
type Client interface {
	Get(ctx context.Context, key string, dest any) error
	// GetDel reads a key and removes it in one command, so a value that must be
	// consumed exactly once cannot be read twice. Get-then-Delete is two commands and
	// two concurrent callers both win.
	GetDel(ctx context.Context, key string, dest any) error
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	Ping() error
	Close() error
}

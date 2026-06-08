package cache

import (
	"context"
	"errors"
	"time"
)

// ErrCacheMiss is returned by Get when the key is absent.
var ErrCacheMiss = errors.New("cache: key not found")

// Client defines methods for caching structured data (e.g., User, Post, ...).
type Client interface {
	Get(ctx context.Context, key string, dest any) error
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	Ping() error
	Close() error
}

// Config provides custom encoding and decoding functions for struct caching.
type Config struct {
	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

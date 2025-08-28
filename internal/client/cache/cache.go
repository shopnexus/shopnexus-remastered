package cache

import (
	"context"
	"time"
)

// TextClient defines methods for caching plain text values.
type TextClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// StructClient defines methods for caching structured data (e.g., User, Post, ...).
type StructClient interface {
	Get(ctx context.Context, key string, dest any) error
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// StructConfig provides custom encoding and decoding functions for struct caching.
type StructConfig struct {
	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

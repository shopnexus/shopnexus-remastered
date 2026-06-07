package cache

import (
	"context"
	"time"

	"github.com/guregu/null/v6"
)

// Client defines methods for caching structured data (e.g., User, Post, ...).
type Client interface {
	Get(ctx context.Context, key string, dest any) error
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	ZAdd(ctx context.Context, key string, value any, score float64) error
	ZRem(ctx context.Context, key string, value any) error
	ZRangeByScore(ctx context.Context, key string, dest any, opts ZRangeOptions) error
	ZRevRangeByScore(ctx context.Context, key string, dest any, opts ZRangeOptions) error

	Ping() error
	Close() error
}

// ZRangeOptions defines optional options for range queries on sorted sets.
type ZRangeOptions struct {
	Start  null.Float
	Stop   null.Float
	Offset null.Int
	Limit  null.Int // Negative limit means no limit (from redis docs)
}

// Config provides custom encoding and decoding functions for struct caching.
type Config struct {
	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

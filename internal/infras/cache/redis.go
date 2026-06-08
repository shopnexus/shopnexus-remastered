package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

var _ Client = (*RedisClient)(nil)

type RedisClient struct {
	config Config
	Client rueidis.Client
}

// NewRedisStructClient initializes a new Redis client for structured data caching.
func NewRedisStructClient(rdb rueidis.Client, cfg Config) (*RedisClient, error) {
	if cfg.Encoder == nil {
		cfg.Encoder = json.Marshal
	}
	if cfg.Decoder == nil {
		cfg.Decoder = json.Unmarshal
	}

	return &RedisClient{
		config: cfg,
		Client: rdb,
	}, nil
}

func (r *RedisClient) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	// rueidis expects string or []byte as value, convert accordingly
	str, err := r.config.Encoder(value)
	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	cmd := r.Client.B().Set().Key(key).Value(string(str))
	if expiration > 0 {
		cmd.Ex(expiration)
	}
	if err := r.Client.Do(ctx, cmd.Build()).Error(); err != nil {
		return fmt.Errorf("failed to set key in Redis: %w", err)
	}
	return nil
}

func (r *RedisClient) Get(ctx context.Context, key string, dest any) error {
	resp := r.Client.Do(ctx, r.Client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return ErrCacheMiss
		}
		return fmt.Errorf("failed to get key from Redis: %w", err)
	}

	str, err := resp.ToString()
	if err != nil {
		return fmt.Errorf("failed to parse get response: %w", err)
	}

	if err = r.config.Decoder([]byte(str), dest); err != nil {
		return fmt.Errorf("failed to decode value: %w", err)
	}

	return nil
}

func (r *RedisClient) Delete(ctx context.Context, key string) error {
	if err := r.Client.Do(ctx, r.Client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("failed to delete key from Redis: %w", err)
	}
	return nil
}

func (r *RedisClient) Exists(ctx context.Context, key string) (bool, error) {
	resp := r.Client.Do(ctx, r.Client.B().Exists().Key(key).Build())
	if err := resp.Error(); err != nil {
		return false, fmt.Errorf("failed to check if key exists in Redis: %w", err)
	}
	count, err := resp.ToInt64()
	if err != nil {
		return false, fmt.Errorf("failed to parse exists response: %w", err)
	}
	return count > 0, nil
}

// Ping reports whether the underlying Redis connection is reachable.
func (r *RedisClient) Ping() error {
	return r.Client.Do(context.Background(), r.Client.B().Ping().Build()).Error()
}

// Close releases the underlying rueidis client.
func (r *RedisClient) Close() error {
	r.Client.Close()
	return nil
}

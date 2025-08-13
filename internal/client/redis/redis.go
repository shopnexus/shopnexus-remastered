package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

type ClientImpl struct {
	Client rueidis.Client
}

type Client interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

type RedisConfig struct {
	Addr     []string
	Password string
	DB       int64
}

// NewClient initializes a new Redis client using application configuration.
func NewClient(cfg RedisConfig) (Client, error) {
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: cfg.Addr,
		// Add password if needed
		Password: cfg.Password,
		// DB selection in rueidis is done via SELECT command after connect
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	// Select DB if not zero
	if cfg.DB != 0 {
		if err := rdb.Do(context.Background(), rdb.B().Select().Index(cfg.DB).Build()).Error(); err != nil {
			return nil, fmt.Errorf("failed to select Redis DB %d: %w", cfg.DB, err)
		}
	}

	return &ClientImpl{Client: rdb}, nil
}

func (r *ClientImpl) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	// rueidis expects string or []byte as value, convert accordingly
	valStr := fmt.Sprintf("%v", value)

	cmd := r.Client.B().Set().Key(key).Value(valStr)
	if expiration > 0 {
		cmd.Ex(expiration)
	}
	if err := r.Client.Do(ctx, cmd.Build()).Error(); err != nil {
		return fmt.Errorf("failed to set key in Redis: %w", err)
	}
	return nil
}

func (r *ClientImpl) Get(ctx context.Context, key string) (string, error) {
	resp := r.Client.Do(ctx, r.Client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if err == rueidis.Nil {
			return "", nil
		}
		return "", fmt.Errorf("failed to get key from Redis: %w", err)
	}

	str, err := resp.ToString()
	if err != nil {
		return "", fmt.Errorf("failed to parse get response: %w", err)
	}

	return str, nil
}

func (r *ClientImpl) Delete(ctx context.Context, key string) error {
	if err := r.Client.Do(ctx, r.Client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("failed to delete key from Redis: %w", err)
	}
	return nil
}

func (r *ClientImpl) Exists(ctx context.Context, key string) (bool, error) {
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

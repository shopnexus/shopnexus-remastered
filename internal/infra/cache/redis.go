package cache

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

var _ Client = (*RedisClient)(nil)

type RedisClient struct {
	Client rueidis.Client
}

// NewRedisClient wraps a rueidis client. Values are JSON: the encoder used to be a configurable
// hook, and every deployment passed the zero value, so the indirection only hid which codec ran.
func NewRedisClient(rdb rueidis.Client) *RedisClient { return &RedisClient{Client: rdb} }

func (r *RedisClient) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	// rueidis takes a string or []byte, so the value is encoded here.
	str, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode cache value for %s: %w", key, err)
	}

	cmd := r.Client.B().Set().Key(key).Value(string(str))
	if expiration > 0 {
		cmd.Ex(expiration)
	}
	if err := r.Client.Do(ctx, cmd.Build()).Error(); err != nil {
		return fmt.Errorf("set redis key %s: %w", key, err)
	}
	return nil
}

func (r *RedisClient) Get(ctx context.Context, key string, dest any) error {
	resp := r.Client.Do(ctx, r.Client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return ErrCacheMiss
		}
		return fmt.Errorf("get redis key %s: %w", key, err)
	}

	str, err := resp.ToString()
	if err != nil {
		return fmt.Errorf("read redis value for %s: %w", key, err)
	}

	if err = json.Unmarshal([]byte(str), dest); err != nil {
		return fmt.Errorf("decode cache value for %s: %w", key, err)
	}

	return nil
}

func (r *RedisClient) GetDel(ctx context.Context, key string, dest any) error {
	resp := r.Client.Do(ctx, r.Client.B().Getdel().Key(key).Build())
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return ErrCacheMiss
		}
		return fmt.Errorf("getdel redis key %s: %w", key, err)
	}

	str, err := resp.ToString()
	if err != nil {
		return fmt.Errorf("read redis value for %s: %w", key, err)
	}

	if err = json.Unmarshal([]byte(str), dest); err != nil {
		return fmt.Errorf("decode cache value for %s: %w", key, err)
	}

	return nil
}

func (r *RedisClient) Delete(ctx context.Context, key string) error {
	if err := r.Client.Do(ctx, r.Client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("delete redis key %s: %w", key, err)
	}
	return nil
}

func (r *RedisClient) Exists(ctx context.Context, key string) (bool, error) {
	resp := r.Client.Do(ctx, r.Client.B().Exists().Key(key).Build())
	if err := resp.Error(); err != nil {
		return false, fmt.Errorf("check redis key %s: %w", key, err)
	}
	count, err := resp.ToInt64()
	if err != nil {
		return false, fmt.Errorf("read redis exists for %s: %w", key, err)
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

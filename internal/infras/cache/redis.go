package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
			return nil
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

func (r *RedisClient) ZAdd(ctx context.Context, key string, value any, score float64) error {
	// Encode the value to string
	str, err := r.config.Encoder(value)
	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	cmd := r.Client.B().Zadd().Key(key).ScoreMember().ScoreMember(score, string(str)).Build()
	if err := r.Client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("failed to zadd to Redis: %w", err)
	}
	return nil
}

func (r *RedisClient) ZRem(ctx context.Context, key string, value any) error {
	// Encode the value to string
	str, err := r.config.Encoder(value)
	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	cmd := r.Client.B().Zrem().Key(key).Member(string(str)).Build()
	if err := r.Client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("failed to zrem from Redis: %w", err)
	}
	return nil
}

// decodeSliceMembers decodes a slice of JSON strings into the provided destination slice.
func (r *RedisClient) decodeSliceMembers(members []string, dest any) error {
	// For other types, decode each member individually from JSON
	// Use reflection to create slice elements and decode each JSON string
	elemType := reflect.TypeOf(dest).Elem().Elem()
	slice := reflect.MakeSlice(reflect.TypeOf(dest).Elem(), len(members), len(members))

	for i, member := range members {
		elem := reflect.New(elemType)
		if err := r.config.Decoder([]byte(member), elem.Interface()); err != nil {
			return fmt.Errorf("failed to decode member %d: %w", i, err)
		}
		slice.Index(i).Set(elem.Elem())
	}

	reflect.ValueOf(dest).Elem().Set(slice)
	return nil
}

func (r *RedisClient) ZRangeByScore(ctx context.Context, key string, dest any, opts ZRangeOptions) error {
	// Build and execute the command using modern ZRANGE with BYSCORE
	var cmd rueidis.Completed
	stopScore := "+inf"
	if opts.Stop.Valid {
		stopScore = fmt.Sprintf("%g", opts.Stop.Float64)
	}
	startScore := "-inf"
	if opts.Start.Valid {
		startScore = fmt.Sprintf("%g", opts.Start.Float64)
	}

	if opts.Limit.Valid && opts.Offset.Valid {
		cmd = r.Client.B().
			Zrange().
			Key(key).
			Min(startScore).
			Max(stopScore).
			Byscore().
			Limit(opts.Offset.Int64, opts.Limit.Int64).
			Build()
	} else {
		cmd = r.Client.B().Zrange().Key(key).Min(startScore).Max(stopScore).Byscore().Build()
	}
	resp := r.Client.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return nil
		}
		return fmt.Errorf("failed to zrange from Redis: %w", err)
	}

	members, err := resp.AsStrSlice()
	if err != nil {
		return fmt.Errorf("failed to parse zrange response: %w", err)
	}

	// If no members found, dest should remain unchanged
	if len(members) == 0 {
		return nil
	}

	return r.decodeSliceMembers(members, dest)
}

func (r *RedisClient) ZRevRangeByScore(ctx context.Context, key string, dest any, opts ZRangeOptions) error {
	// Build and execute the command using modern ZRANGE with REV and BYSCORE
	var cmd rueidis.Completed

	stopScore := "-inf"
	if opts.Stop.Valid {
		stopScore = fmt.Sprintf("%g", opts.Stop.Float64)
	}
	startScore := "+inf"
	if opts.Start.Valid {
		startScore = fmt.Sprintf("%g", opts.Start.Float64)
	}

	if opts.Limit.Valid && opts.Offset.Valid {
		cmd = r.Client.B().
			Zrange().
			Key(key).
			Min(startScore).
			Max(stopScore).
			Byscore().
			Rev().
			Limit(opts.Offset.Int64, opts.Limit.Int64).
			Build()
	} else {
		cmd = r.Client.B().Zrange().Key(key).Min(startScore).Max(stopScore).Byscore().Rev().Build()
	}
	resp := r.Client.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return nil
		}
		return fmt.Errorf("failed to zrange with rev from Redis: %w", err)
	}

	members, err := resp.AsStrSlice()
	if err != nil {
		return fmt.Errorf("failed to parse zrange rev response: %w", err)
	}

	// If no members found, dest should remain unchanged
	if len(members) == 0 {
		return nil
	}

	return r.decodeSliceMembers(members, dest)
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

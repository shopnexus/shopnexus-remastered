package rankedset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/rueidis"
)

var _ Client = (*RedisRankedSet)(nil)

type RedisRankedSet struct {
	config Config
	Client rueidis.Client
}

// NewRedisRankedSet initializes a Redis-backed ranked set over a sorted set.
func NewRedisRankedSet(rdb rueidis.Client, cfg Config) (*RedisRankedSet, error) {
	if cfg.Encoder == nil {
		cfg.Encoder = json.Marshal
	}
	if cfg.Decoder == nil {
		cfg.Decoder = json.Unmarshal
	}

	return &RedisRankedSet{
		config: cfg,
		Client: rdb,
	}, nil
}

func (r *RedisRankedSet) Add(ctx context.Context, key string, value any, score float64) error {
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

// Ping reports whether the underlying Redis connection is reachable.
func (r *RedisRankedSet) Ping() error {
	return r.Client.Do(context.Background(), r.Client.B().Ping().Build()).Error()
}

// Close releases the underlying rueidis client.
func (r *RedisRankedSet) Close() error {
	r.Client.Close()
	return nil
}

// Delete removes the whole ranked set at key.
func (r *RedisRankedSet) Delete(ctx context.Context, key string) error {
	if err := r.Client.Do(ctx, r.Client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("failed to delete key from Redis: %w", err)
	}
	return nil
}

// TopByScore returns members ordered by score, highest first.
func (r *RedisRankedSet) TopByScore(ctx context.Context, key string, dest any, opts RangeOptions) error {
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

	return decodeMembers(r.config.Decoder, members, dest)
}

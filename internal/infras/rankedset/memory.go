package rankedset

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

var _ Client = (*MemoryRankedSet)(nil)

// MemoryRankedSet is an in-process ranked set backed by per-key score maps.
// Members dedupe by encoded value, mirroring Redis ZADD. Intended for dev /
// single-instance use — state is process-local and lost on restart.
type MemoryRankedSet struct {
	config Config
	mu     sync.Mutex
	sets   map[string]map[string]float64 // key -> encoded member -> score
}

// NewMemoryRankedSet initializes an in-process ranked set.
func NewMemoryRankedSet(cfg Config) *MemoryRankedSet {
	if cfg.Encoder == nil {
		cfg.Encoder = json.Marshal
	}
	if cfg.Decoder == nil {
		cfg.Decoder = json.Unmarshal
	}

	return &MemoryRankedSet{
		config: cfg,
		sets:   make(map[string]map[string]float64),
	}
}

func (m *MemoryRankedSet) Add(ctx context.Context, key string, value any, score float64) error {
	str, err := m.config.Encoder(value)
	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.sets[key]
	if set == nil {
		set = make(map[string]float64)
		m.sets[key] = set
	}
	set[string(str)] = score
	return nil
}

// Ping always succeeds; the store is in-process.
func (m *MemoryRankedSet) Ping() error { return nil }

// Close is a no-op; there is no connection to release.
func (m *MemoryRankedSet) Close() error { return nil }

func (m *MemoryRankedSet) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sets, key)
	return nil
}

// TopByScore returns members ordered by score, highest first, within the
// optional [Stop, Start] score bounds and offset/limit window.
func (m *MemoryRankedSet) TopByScore(ctx context.Context, key string, dest any, opts RangeOptions) error {
	type scored struct {
		member string
		score  float64
	}

	m.mu.Lock()
	set := m.sets[key]
	arr := make([]scored, 0, len(set))
	for member, score := range set {
		arr = append(arr, scored{member, score})
	}
	m.mu.Unlock()

	// Score bounds: Start is the upper bound (default +inf), Stop the lower (default -inf).
	arr2 := arr[:0]
	for _, s := range arr {
		if opts.Start.Valid && s.score > opts.Start.Float64 {
			continue
		}
		if opts.Stop.Valid && s.score < opts.Stop.Float64 {
			continue
		}
		arr2 = append(arr2, s)
	}
	arr = arr2

	sort.Slice(arr, func(i, j int) bool { return arr[i].score > arr[j].score })

	// offset/limit window (negative limit = no limit, per redis docs).
	if opts.Offset.Valid && opts.Limit.Valid {
		offset := int(opts.Offset.Int64)
		if offset >= len(arr) {
			arr = nil
		} else {
			arr = arr[offset:]
		}
		if limit := opts.Limit.Int64; limit >= 0 && int(limit) < len(arr) {
			arr = arr[:limit]
		}
	}

	if len(arr) == 0 {
		return nil
	}

	members := make([]string, len(arr))
	for i, s := range arr {
		members[i] = s.member
	}
	return decodeMembers(m.config.Decoder, members, dest)
}

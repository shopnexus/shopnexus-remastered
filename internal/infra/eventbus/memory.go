package eventbus

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClosed is returned by Publish after Close.
var ErrClosed = errors.New("eventbus: closed")

// memorySubBuffer is the per-subscription channel capacity. A full buffer
// blocks Publish (backpressure) until the consumer catches up.
const memorySubBuffer = 1024

// memorySub is one subscription: a queue drained by its own consumer goroutine.
type memorySub struct {
	ch chan []byte
}

// memoryGroup holds a group's subscriptions and a round-robin cursor for
// competing-consumer delivery within the group.
type memoryGroup struct {
	subs []*memorySub
	next atomic.Uint64
}

var _ Client = (*Memory)(nil)

// Memory is an in-process Client. Each subscription runs a consumer
// goroutine that batches payloads per SubscribeOptions; handler errors are
// logged and dropped.
type Memory struct {
	mu        sync.RWMutex
	topics    map[string]map[string]*memoryGroup // topic -> group -> subscriptions
	closed    bool
	inflight  sync.WaitGroup // published payloads not yet handled
	consumers sync.WaitGroup // consumer goroutines
	logger    *slog.Logger
}

func NewMemory(logger *slog.Logger) *Memory {
	if logger == nil {
		logger = slog.Default()
	}
	m := new(Memory)
	m.topics = make(map[string]map[string]*memoryGroup)
	m.logger = logger
	return m
}

func (m *Memory) Subscribe(topic, group string, h Handler, opts SubscribeOptions) {
	if opts.BatchSize < 1 {
		opts.BatchSize = 1
	}
	sub := &memorySub{ch: make(chan []byte, memorySubBuffer)}

	m.mu.Lock()
	groups, ok := m.topics[topic]
	if !ok {
		groups = make(map[string]*memoryGroup)
		m.topics[topic] = groups
	}
	g, ok := groups[group]
	if !ok {
		g = new(memoryGroup)
		groups[group] = g
	}
	g.subs = append(g.subs, sub)
	m.mu.Unlock()

	m.consumers.Go(func() {
		m.consume(topic, group, sub, h, opts)
	})
}

// Publish enqueues payload to one subscription per group (round-robin).
// Sends happen under the read lock so Close cannot close a channel mid-send.
func (m *Memory) Publish(_ context.Context, topic string, payload []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrClosed
	}
	for _, g := range m.topics[topic] {
		i := (g.next.Add(1) - 1) % uint64(len(g.subs))
		m.inflight.Add(1)
		g.subs[i].ch <- payload
	}
	return nil
}

// consume drains a subscription batch by batch and hands each batch to h.
func (m *Memory) consume(topic, group string, sub *memorySub, h Handler, opts SubscribeOptions) {
	// Batches span multiple publishes, so handlers get a fresh context rather
	// than any single publisher's.
	ctx := context.Background()
	for {
		first, ok := <-sub.ch
		if !ok {
			return
		}
		batch := fillBatch(sub, first, opts)
		if err := h(ctx, batch); err != nil {
			m.logger.ErrorContext(ctx, "eventbus: handler failed",
				"topic", topic, "group", group, "batch", len(batch), "err", err)
		}
		m.inflight.Add(-len(batch))
	}
}

// fillBatch grows a batch from first until BatchSize is reached, Linger
// expires, or the subscription closes — whichever comes first.
func fillBatch(sub *memorySub, first []byte, opts SubscribeOptions) [][]byte {
	batch := [][]byte{first}
	if opts.BatchSize <= 1 {
		return batch
	}
	timer := time.NewTimer(opts.Linger)
	defer timer.Stop()
	for len(batch) < opts.BatchSize {
		select {
		case payload, more := <-sub.ch:
			if !more {
				return batch
			}
			batch = append(batch, payload)
		case <-timer.C:
			return batch
		}
	}
	return batch
}

// Wait blocks until every published payload has been handled. Use in tests.
func (m *Memory) Wait() {
	m.inflight.Wait()
}

// Ping reports whether the bus accepts publishes.
func (m *Memory) Ping() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

// Close rejects further publishes, lets consumers drain their queues, and
// returns once they exit. Use on shutdown.
func (m *Memory) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	for _, groups := range m.topics {
		for _, g := range groups {
			for _, sub := range g.subs {
				close(sub.ch)
			}
		}
	}
	m.mu.Unlock()
	m.consumers.Wait()
	return nil
}

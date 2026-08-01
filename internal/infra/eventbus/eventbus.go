// Package eventbus is the type-safe pub/sub seam. Topics bind a name to a
// payload type at compile time; transports move raw bytes so implementations
// (in-memory, redis, ...) stay codec-agnostic.
package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Client interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(topic, group string, h Handler, opts SubscribeOptions)

	Ping() error
	Close() error
}

// Handler consumes a batch of raw payloads delivered by a Transport.
// Without batch options the batch always has exactly one payload.
type Handler func(ctx context.Context, payloads [][]byte) error

// SubscribeOptions controls how a Transport groups payloads before delivery.
type SubscribeOptions struct {
	BatchSize int           // flush once the batch holds this many payloads (min 1)
	Linger    time.Duration // max wait after the first payload before flushing a partial batch
}

// SubscribeOption configures a subscription. See WithBatchSize, WithLinger.
type SubscribeOption func(*SubscribeOptions)

// WithBatchSize delivers payloads in batches of up to n. Combine with
// WithLinger so partial batches still flush; without it a partial batch only
// flushes when more payloads arrive.
func WithBatchSize(n int) SubscribeOption {
	return func(o *SubscribeOptions) {
		if n > 1 {
			o.BatchSize = n
		}
	}
}

// WithLinger flushes a partial batch d after its first payload arrived.
func WithLinger(d time.Duration) SubscribeOption {
	return func(o *SubscribeOptions) {
		if d > 0 {
			o.Linger = d
		}
	}
}

// Topic binds a name to payload type T at compile time.
type Topic[T any] struct {
	Name string
}

func NewTopic[T any](name string) Topic[T] {
	return Topic[T]{Name: name}
}

// Publish marshals payload and fans it out to every group subscribed to topic.
func Publish[T any](ctx context.Context, c Client, topic Topic[T], payload T) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("eventbus: marshal %s: %w", topic.Name, err)
	}
	return c.Publish(ctx, topic.Name, data)
}

// Subscribe registers h under group for topic, invoked once per payload.
func Subscribe[T any](c Client, topic Topic[T], group string, h func(ctx context.Context, payload T) error) {
	SubscribeBatch(c, topic, group, func(ctx context.Context, payloads []T) error {
		for _, p := range payloads {
			if err := h(ctx, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// SubscribeBatch registers h under group for topic, invoked with batches of
// payloads. Batch shape is controlled by WithBatchSize / WithLinger: a batch
// flushes when it reaches BatchSize or when Linger has passed since its first
// payload, whichever comes first. Without options batches hold one payload.
func SubscribeBatch[T any](
	c Client,
	topic Topic[T],
	group string,
	h func(ctx context.Context, payloads []T) error,
	opts ...SubscribeOption,
) {
	options := SubscribeOptions{BatchSize: 1, Linger: 0}
	for _, opt := range opts {
		opt(&options)
	}
	c.Subscribe(topic.Name, group, func(ctx context.Context, raws [][]byte) error {
		payloads := make([]T, len(raws))
		for i, raw := range raws {
			if err := json.Unmarshal(raw, &payloads[i]); err != nil {
				return fmt.Errorf("eventbus: unmarshal %s: %w", topic.Name, err)
			}
		}
		return h(ctx, payloads)
	}, options)
}

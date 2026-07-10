package pubsub

import (
	"context"
	"time"
)

type Client interface {
	Group(name string) Client
	Publish(topic string, value any) error
	Subscribe(topic string, handler func(msg *MessageDecoder) error) error
	Close() error
}

// DecodeWrap is a helper to wrap a function with a specific parameter type into a handler function.
func DecodeWrap[T any](f func(ctx context.Context, params T) error) func(msg *MessageDecoder) error {
	return func(msg *MessageDecoder) error {
		var params T
		if err := msg.Decode(&params); err != nil {
			return err
		}
		return f(context.Background(), params)
	}
}

// Config holds configuration values for the pub/sub connection.
type Config struct {
	Timeout time.Duration
	Brokers []string

	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

package pubsub

import (
	"context"
	"time"
)

type Client interface {
	Publish(ctx context.Context, topic string, value any) error
	Subscribe(ctx context.Context, topic string, handler func(msg *MessageDecoder) error) error
	Close() error
}

// Config holds configuration values for the Kafka connection.
type Config struct {
	Group   string
	Timeout time.Duration
	Brokers []string

	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

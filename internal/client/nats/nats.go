package nats

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

type ClientImpl struct {
	conn *nats.Conn
}

type Client interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, handler func(msg *nats.Msg)) (*nats.Subscription, error)
	Close()
}

// NATSConfig holds config values for the NATS connection.
type Config struct {
	URL     string
	Timeout time.Duration
}

// NewClient creates and connects a new NATS client.
func NewClient(cfg Config) (Client, error) {
	opts := []nats.Option{
		nats.Timeout(cfg.Timeout),
		nats.Name("NATS Client"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(10),
	}

	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return &ClientImpl{conn: conn}, nil
}

// Publish sends a message to the given subject.
func (n *ClientImpl) Publish(subject string, data []byte) error {
	if err := n.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("failed to publish to subject %s: %w", subject, err)
	}
	return nil
}

// Subscribe listens for messages on a subject and handles them with the given callback.
func (n *ClientImpl) Subscribe(subject string, handler func(msg *nats.Msg)) (*nats.Subscription, error) {
	sub, err := n.conn.Subscribe(subject, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}
	return sub, nil
}

// Close cleanly shuts down the NATS connection.
func (n *ClientImpl) Close() {
	n.conn.Close()
}

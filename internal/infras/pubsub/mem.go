package pubsub

import (
	"context"
	"errors"
	"sync"
)

// subscription represents a single subscription.
type subscription struct {
	handler func(msg *MessageDecoder) error
	done    chan struct{}
}

// MemoryClient implements the Client interface for in-memory pub/sub.
type MemoryClient struct {
	config        Config
	group         string
	subscriptions map[string][]*subscription
	mu            sync.RWMutex
	closed        bool
}

// NewMemoryClient creates a new in-memory pub/sub client.
func NewMemoryClient(config Config) *MemoryClient {
	return &MemoryClient{
		config:        config,
		subscriptions: make(map[string][]*subscription),
	}
}

// Group returns a new MemoryClient scoped to the given consumer group.
func (c *MemoryClient) Group(name string) Client {
	return &MemoryClient{
		config:        c.config,
		group:         name,
		subscriptions: c.subscriptions, // share subscriptions map
		mu:            sync.RWMutex{},
		closed:        c.closed,
	}
}

// Publish sends a message to all subscribers of the given topic.
func (c *MemoryClient) Publish(topic string, value any) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return errors.New("client is closed")
	}

	subs := c.subscriptions[topic]
	c.mu.RUnlock()

	if len(subs) == 0 {
		return nil // No subscribers, nothing to do
	}

	// Encode the value
	data, err := c.config.Encoder(value)
	if err != nil {
		return err
	}

	// Create message decoder
	msg := NewMessageDecoder(data, c.config.Decoder)

	// Send to all subscribers
	for _, sub := range subs {
		select {
		case <-sub.done:
			continue // Skip closed subscriptions
		default:
			// Handle message in a separate goroutine to prevent blocking
			go func(s *subscription) {
				defer func() {
					if r := recover(); r != nil {
						// Log panic if you have logging setup
						// log.Printf("Handler panic: %v", r)
					}
				}()

				// Create a timeout context for the handler
				ctx := context.Background()
				if c.config.Timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, c.config.Timeout)
					defer cancel()
				}

				// Call handler with timeout
				done := make(chan error, 1)
				go func() {
					done <- s.handler(msg)
				}()

				select {
				case <-ctx.Done():
					// Handler timed out or context cancelled
				case <-done:
					// Handler completed
				}
			}(sub)
		}
	}

	return nil
}

// Subscribe registers a handler for messages on the given topic.
func (c *MemoryClient) Subscribe(topic string, handler func(msg *MessageDecoder) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("client is closed")
	}

	sub := &subscription{
		handler: handler,
		done:    make(chan struct{}),
	}

	c.subscriptions[topic] = append(c.subscriptions[topic], sub)

	return nil
}

// Close closes the client and all active subscriptions.
func (c *MemoryClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// Close all subscriptions
	for _, subs := range c.subscriptions {
		for _, sub := range subs {
			close(sub.done)
		}
	}

	// Clear subscriptions
	c.subscriptions = make(map[string][]*subscription)

	return nil
}

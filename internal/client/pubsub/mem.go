package pubsub

import (
	"context"
	"fmt"
	"sync"
)

// subscription represents a single subscription
type subscription struct {
	handler func(msg *MessageDecoder) error
	done    chan struct{}
}

// MemoryClient implements the Client interface for in-memory pub/sub
type MemoryClient struct {
	config        Config
	subscriptions map[string][]*subscription
	mu            sync.RWMutex
	closed        bool
}

// NewMemoryClient creates a new in-memory pub/sub client
func NewMemoryClient(config Config) *MemoryClient {
	return &MemoryClient{
		config:        config,
		subscriptions: make(map[string][]*subscription),
	}
}

// Publish sends a message to all subscribers of the given topic
func (c *MemoryClient) Publish(ctx context.Context, topic string, value any) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("client is closed")
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
		case <-ctx.Done():
			return ctx.Err()
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
				handlerCtx := ctx
				if c.config.Timeout > 0 {
					var cancel context.CancelFunc
					handlerCtx, cancel = context.WithTimeout(ctx, c.config.Timeout)
					defer cancel()
				}

				// Call handler with timeout
				done := make(chan error, 1)
				go func() {
					done <- s.handler(msg)
				}()

				select {
				case <-handlerCtx.Done():
					// Handler timed out or context cancelled
				case <-done:
					// Handler completed
				}
			}(sub)
		}
	}

	return nil
}

// Subscribe registers a handler for messages on the given topic
func (c *MemoryClient) Subscribe(ctx context.Context, topic string, handler func(msg *MessageDecoder) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("client is closed")
	}

	sub := &subscription{
		handler: handler,
		done:    make(chan struct{}),
	}

	c.subscriptions[topic] = append(c.subscriptions[topic], sub)

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		close(sub.done)
		c.removeSubscription(topic, sub)
	}()

	return nil
}

// removeSubscription removes a subscription from the topic
func (c *MemoryClient) removeSubscription(topic string, targetSub *subscription) {
	c.mu.Lock()
	defer c.mu.Unlock()

	subs := c.subscriptions[topic]
	for i, sub := range subs {
		if sub == targetSub {
			// Remove subscription by swapping with last element
			c.subscriptions[topic][i] = c.subscriptions[topic][len(subs)-1]
			c.subscriptions[topic] = c.subscriptions[topic][:len(subs)-1]
			break
		}
	}

	// Clean up empty topic
	if len(c.subscriptions[topic]) == 0 {
		delete(c.subscriptions, topic)
	}
}

// Close closes the client and all active subscriptions
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

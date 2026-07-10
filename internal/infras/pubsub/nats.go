package pubsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill"
	wnats "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/bytedance/sonic"
)

type NatsClient struct {
	config    NatsConfig
	publisher *wnats.Publisher
	subs      []*wnats.Subscriber
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
}

type NatsConfig struct {
	Config

	Group string // Queue group / durable consumer name
}

// NewNatsClient creates a new NATS JetStream client using Watermill.
func NewNatsClient(cfg NatsConfig) (*NatsClient, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("at least one broker must be specified")
	}
	if cfg.Decoder == nil {
		cfg.Decoder = sonic.Unmarshal
	}
	if cfg.Encoder == nil {
		cfg.Encoder = sonic.Marshal
	}

	marshaler := &JSONMarshaler{
		Encoder: cfg.Encoder,
		Decoder: cfg.Decoder,
	}

	url := fmt.Sprintf("nats://%s", cfg.Brokers[0])
	logger := watermill.NewStdLogger(false, false)

	publisher, err := wnats.NewPublisher(
		wnats.PublisherConfig{
			URL:       url,
			Marshaler: marshaler,
			JetStream: wnats.JetStreamConfig{
				AutoProvision: true,
			},
		},
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create NATS publisher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &NatsClient{
		config:    cfg,
		publisher: publisher,
		ctx:       ctx,
		cancel:    cancel,
	}, nil
}

// Group returns a new NatsClient that shares the same publisher but scoped to a consumer group.
func (n *NatsClient) Group(name string) Client {
	newConfig := n.config
	newConfig.Group = name
	return &NatsClient{
		config:    newConfig,
		publisher: n.publisher, // share the publisher
		subs:      nil,         // new client gets its own subscriber list
		ctx:       n.ctx,
		cancel:    n.cancel,
	}
}

// Publish sends a message to the given topic.
func (n *NatsClient) Publish(topic string, value any) error {
	encodedValue, err := n.config.Encoder(value)
	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	msg := message.NewMessage(watermill.NewUUID(), encodedValue)

	if err = n.publisher.Publish(topic, msg); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// Subscribe listens for messages on a topic and handles them with the given callback.
func (n *NatsClient) Subscribe(topic string, handler func(msg *MessageDecoder) error) error {
	if n.config.Group == "" {
		slog.Warn("Subscribing to topic without a consumer group", slog.String("topic", topic))
	}

	marshaler := &JSONMarshaler{
		Encoder: n.config.Encoder,
		Decoder: n.config.Decoder,
	}

	url := fmt.Sprintf("nats://%s", n.config.Brokers[0])
	logger := watermill.NewStdLogger(false, false)

	subscriber, err := wnats.NewSubscriber(
		wnats.SubscriberConfig{
			URL:              url,
			QueueGroupPrefix: n.config.Group,
			Unmarshaler:      marshaler,
			JetStream: wnats.JetStreamConfig{
				AutoProvision: true,
				DurablePrefix: n.config.Group,
			},
		},
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create NATS subscriber: %w", err)
	}

	n.mu.Lock()
	n.subs = append(n.subs, subscriber)
	n.mu.Unlock()

	messages, err := subscriber.Subscribe(n.ctx, topic)
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}

	go func() {
		for {
			select {
			case msg, ok := <-messages:
				if !ok {
					return
				}
				if err := handler(NewMessageDecoder(msg.Payload, n.config.Decoder)); err != nil {
					msg.Nack()
					slog.Error("Error handling message", slog.String("topic", topic), slog.Any("error", err))
				} else {
					msg.Ack()
				}
			case <-n.ctx.Done():
				slog.Info("Context cancelled, stopping subscription", slog.String("topic", topic))
				return
			}
		}
	}()

	return nil
}

// Close cleanly shuts down the NATS client, all subscribers, and the publisher.
func (n *NatsClient) Close() error {
	n.cancel()

	n.mu.Lock()
	defer n.mu.Unlock()

	var firstErr error

	for _, sub := range n.subs {
		if err := sub.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	n.subs = nil

	if err := n.publisher.Close(); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

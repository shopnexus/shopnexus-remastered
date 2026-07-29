package eventbus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	natsStreamName    = "EVENTBUS"
	natsSubjectPrefix = "bus."
	natsMaxAge        = 24 * time.Hour  // buffer horizon: payloads nobody consumed by then are dropped
	natsMaxDeliver    = 5               // stop redelivering a payload no handler can process
	natsMaxAckPending = 10000           // cap on unacked payloads per consumer
	natsFetchWait     = 5 * time.Second // Fetch wait when no Linger is set, so Close is noticed
	natsRetryBackoff  = time.Second     // wait after a transient fetch/consumer error
	natsDrainTimeout  = 5 * time.Second // how long Close waits for buffered publishes
)

var _ Client = (*NATS)(nil)

// NATS is a Client backed by NATS JetStream: every topic is a subject
// (bus.<topic>) inside one stream, and every subscription group gets a durable
// pull consumer. Batching maps straight onto Fetch(BatchSize, MaxWait(Linger)):
// a fetch returns once BatchSize payloads are ready, or when Linger elapses
// with a partial batch. Payloads are persisted before delivery, so an unacked
// batch (handler error, crash) is redelivered — up to natsMaxDeliver attempts.
//
// Publish is asynchronous: it returns once the payload is buffered locally, so
// a caller on a request path never waits for the server ack. That suits
// best-effort telemetry; a server-side failure reaches the error handler (which
// logs) rather than the caller.
type NATS struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// DialNATS connects to url, ensures the stream exists, and returns a
// JetStream-backed Client. The returned NATS owns the connection: Close closes it.
func DialNATS(ctx context.Context, url string, logger *slog.Logger) (*NATS, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("eventbus: connect nats %s: %w", url, err)
	}
	n, err := NewNATS(ctx, conn, logger)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return n, nil
}

// NewNATS wraps an existing connection, creating the stream if it is missing.
// The returned NATS owns conn (Close closes it).
func NewNATS(ctx context.Context, conn *nats.Conn, logger *slog.Logger) (*NATS, error) {
	if logger == nil {
		logger = slog.Default()
	}
	js, err := jetstream.New(conn, jetstream.WithPublishAsyncErrHandler(
		func(_ jetstream.JetStream, msg *nats.Msg, err error) {
			// The publisher already moved on, so this is the only place a failed
			// async publish can be reported.
			logger.Warn("eventbus: async publish failed", "subject", msg.Subject, "err", err)
		}))
	if err != nil {
		return nil, fmt.Errorf("eventbus: jetstream context: %w", err)
	}
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      natsStreamName,
		Subjects:  []string{natsSubjectPrefix + ">"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Discard:   jetstream.DiscardOld,
		MaxAge:    natsMaxAge,
	})
	if err != nil {
		return nil, fmt.Errorf("eventbus: create stream %s: %w", natsStreamName, err)
	}
	n := new(NATS)
	n.conn = conn
	n.js = js
	n.stream = stream
	n.logger = logger
	n.ctx, n.cancel = context.WithCancel(context.Background())
	return n, nil
}

// Publish buffers payload for asynchronous delivery to the stream. It does not
// wait for the server ack — see the type comment.
func (n *NATS) Publish(_ context.Context, topic string, payload []byte) error {
	if _, err := n.js.PublishAsync(natsSubjectPrefix+topic, payload); err != nil {
		return fmt.Errorf("eventbus: publish %s: %w", topic, err)
	}
	return nil
}

func (n *NATS) Subscribe(topic, group string, h Handler, opts SubscribeOptions) {
	if opts.BatchSize < 1 {
		opts.BatchSize = 1
	}
	n.wg.Go(func() {
		n.consume(topic, group, h, opts)
	})
}

func (n *NATS) consume(topic, group string, h Handler, opts SubscribeOptions) {
	cons, ok := n.ensureConsumer(topic, group)
	if !ok {
		return
	}
	wait := natsFetchWait
	if opts.Linger > 0 {
		wait = opts.Linger
	}
	for n.ctx.Err() == nil {
		batch, err := cons.Fetch(opts.BatchSize, jetstream.FetchMaxWait(wait))
		if err != nil {
			if n.ctx.Err() != nil {
				return
			}
			n.logger.ErrorContext(n.ctx, "eventbus: fetch failed", "topic", topic, "group", group, "err", err)
			time.Sleep(natsRetryBackoff)
			continue
		}
		msgs := make([]jetstream.Msg, 0, opts.BatchSize)
		payloads := make([][]byte, 0, opts.BatchSize)
		for msg := range batch.Messages() {
			msgs = append(msgs, msg)
			payloads = append(payloads, msg.Data())
		}
		if err := batch.Error(); err != nil {
			n.logger.ErrorContext(n.ctx, "eventbus: fetch batch failed", "topic", topic, "group", group, "err", err)
		}
		if len(payloads) == 0 {
			continue // Linger expired with nothing pending
		}
		n.deliver(topic, group, msgs, payloads, h)
	}
}

// deliver hands a batch to h and acks it on success. On failure the batch is
// nacked, so JetStream redelivers it instead of dropping the payloads.
func (n *NATS) deliver(topic, group string, msgs []jetstream.Msg, payloads [][]byte, h Handler) {
	if err := h(context.Background(), payloads); err != nil {
		n.logger.ErrorContext(n.ctx, "eventbus: handler failed",
			"topic", topic, "group", group, "batch", len(payloads), "err", err)
		n.settle(topic, group, msgs, jetstream.Msg.Nak, "nak")
		return
	}
	n.settle(topic, group, msgs, jetstream.Msg.Ack, "ack")
}

// settle applies ack or nak to every message, logging once for the batch: a
// failure here only means the batch is redelivered, which handlers tolerate.
func (n *NATS) settle(topic, group string, msgs []jetstream.Msg, apply func(jetstream.Msg) error, op string) {
	var failed int
	var first error
	for _, msg := range msgs {
		if err := apply(msg); err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed > 0 {
		n.logger.ErrorContext(n.ctx, "eventbus: "+op+" failed",
			"topic", topic, "group", group, "messages", failed, "err", first)
	}
}

// ensureConsumer creates (or reuses) the group's durable consumer, retrying on
// transient errors until the transport closes.
func (n *NATS) ensureConsumer(topic, group string) (jetstream.Consumer, bool) {
	cfg := jetstream.ConsumerConfig{
		Durable:       natsConsumerName(group, topic),
		FilterSubject: natsSubjectPrefix + topic,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    natsMaxDeliver,
		MaxAckPending: natsMaxAckPending,
	}
	for n.ctx.Err() == nil {
		cons, err := n.stream.CreateOrUpdateConsumer(n.ctx, cfg)
		if err == nil {
			return cons, true
		}
		n.logger.ErrorContext(n.ctx, "eventbus: create consumer failed", "topic", topic, "group", group, "err", err)
		time.Sleep(natsRetryBackoff)
	}
	return nil, false
}

// natsConsumerName builds a durable name from group+topic: durables may not
// contain dots, wildcards, path separators or whitespace.
func natsConsumerName(group, topic string) string {
	r := strings.NewReplacer(".", "_", "*", "_", ">", "_", "/", "_", `\`, "_", " ", "_")
	return r.Replace(group + "_" + topic)
}

// Ping reports whether the NATS connection is reachable.
func (n *NATS) Ping() error {
	if _, err := n.conn.RTT(); err != nil {
		return fmt.Errorf("eventbus: nats rtt: %w", err)
	}
	return nil
}

// Close flushes buffered publishes, stops all consumers, waits for the batch in
// flight, then closes the connection (the bus owns it). Payloads already in the
// stream but not yet consumed survive: the durable consumers resume after restart.
func (n *NATS) Close() error {
	select {
	case <-n.js.PublishAsyncComplete():
	case <-time.After(natsDrainTimeout):
		n.logger.Warn("eventbus: buffered publishes not flushed before close")
	}
	n.cancel()
	n.wg.Wait()
	n.conn.Close()
	return nil
}

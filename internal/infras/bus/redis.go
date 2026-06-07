package bus

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/rueidis"
)

const (
	redisStreamPrefix = "bus:"
	redisPayloadField = "payload"
	redisMaxLen       = "100000"        // approximate per-stream cap (XADD MAXLEN ~)
	redisPollBlock    = 5 * time.Second // XREADGROUP block when no Linger is set, so Close is noticed
	redisRetryBackoff = time.Second     // wait after a transient read/group error
)

var _ Client = (*Redis)(nil)

// Redis is a Client backed by Redis Streams: one stream per topic, one
// consumer group per subscription group. Batching maps to XREADGROUP
// COUNT/BLOCK: a read returns as soon as payloads are available, so Linger
// caps the wait for the first payload rather than stretching a partial batch.
// Unacked payloads (handler error, crash) are re-read on restart via each
// consumer's pending list.
type Redis struct {
	client rueidis.Client
	logger *slog.Logger
	host   string
	seq    atomic.Uint64 // per-subscription suffix to keep consumer names stable yet unique
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRedis(client rueidis.Client, logger *slog.Logger) *Redis {
	if logger == nil {
		logger = slog.Default()
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "bus"
	}
	r := new(Redis)
	r.client = client
	r.logger = logger
	r.host = host
	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r
}

func (r *Redis) Publish(ctx context.Context, topic string, payload []byte) error {
	cmd := r.client.B().Xadd().
		Key(redisStreamPrefix+topic).
		Maxlen().Almost().Threshold(redisMaxLen).
		Id("*").
		FieldValue().FieldValue(redisPayloadField, rueidis.BinaryString(payload)).
		Build()
	if err := r.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("bus: xadd %s: %w", topic, err)
	}
	return nil
}

func (r *Redis) Subscribe(topic, group string, h Handler, opts SubscribeOptions) {
	if opts.BatchSize < 1 {
		opts.BatchSize = 1
	}
	// Stable across restarts (hostname + registration order) so a restarted
	// process re-reads its own pending entries; unique per subscription so
	// in-process competing consumers don't share a pending list.
	consumer := r.host + "-" + strconv.FormatUint(r.seq.Add(1), 10)
	r.wg.Go(func() {
		r.consume(redisStreamPrefix+topic, group, consumer, h, opts)
	})
}

func (r *Redis) consume(stream, group, consumer string, h Handler, opts SubscribeOptions) {
	if !r.ensureGroup(stream, group) {
		return
	}
	// "0" replays this consumer's pending (delivered, never acked) backlog
	// first; once drained, ">" reads new entries.
	readID := "0"
	for r.ctx.Err() == nil {
		entries, err := r.read(stream, group, consumer, readID, opts)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			if !rueidis.IsRedisNil(err) {
				r.logger.ErrorContext(r.ctx, "bus: read failed", "stream", stream, "group", group, "err", err)
				time.Sleep(redisRetryBackoff)
			}
			continue
		}
		if len(entries) == 0 {
			readID = ">" // pending backlog drained
			continue
		}
		if readID != ">" {
			readID = entries[len(entries)-1].ID // advance the backlog cursor
		}
		r.deliver(stream, group, entries, h)
	}
}

// deliver hands a batch to h and acks on success. No ack on error: entries
// stay pending and are replayed on restart.
func (r *Redis) deliver(stream, group string, entries []rueidis.XRangeEntry, h Handler) {
	payloads := make([][]byte, 0, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if v, ok := e.FieldValues[redisPayloadField]; ok {
			payloads = append(payloads, []byte(v))
			ids = append(ids, e.ID)
		}
	}
	if len(payloads) == 0 {
		return
	}
	if err := h(context.Background(), payloads); err != nil {
		r.logger.ErrorContext(r.ctx, "bus: handler failed",
			"stream", stream, "group", group, "batch", len(payloads), "err", err)
		return
	}
	r.ack(stream, group, ids)
}

// ensureGroup creates the consumer group (and stream) if missing, retrying on
// transient errors until the transport closes.
func (r *Redis) ensureGroup(stream, group string) bool {
	for r.ctx.Err() == nil {
		cmd := r.client.B().XgroupCreate().Key(stream).Group(group).Id("$").Mkstream().Build()
		err := r.client.Do(r.ctx, cmd).Error()
		if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
			return true
		}
		r.logger.ErrorContext(r.ctx, "bus: create group failed", "stream", stream, "group", group, "err", err)
		time.Sleep(redisRetryBackoff)
	}
	return false
}

func (r *Redis) read(stream, group, consumer, readID string, opts SubscribeOptions) ([]rueidis.XRangeEntry, error) {
	block := redisPollBlock
	if opts.Linger > 0 {
		block = opts.Linger
	}
	cmd := r.client.B().Xreadgroup().
		Group(group, consumer).
		Count(int64(opts.BatchSize)).
		Block(block.Milliseconds()).
		Streams().Key(stream).Id(readID).
		Build()
	res, err := r.client.Do(r.ctx, cmd).AsXRead()
	if err != nil {
		return nil, err
	}
	return res[stream], nil
}

func (r *Redis) ack(stream, group string, ids []string) {
	cmd := r.client.B().Xack().Key(stream).Group(group).Id(ids...).Build()
	if err := r.client.Do(r.ctx, cmd).Error(); err != nil {
		// Failed ack means redelivery on restart — handlers must tolerate it.
		r.logger.ErrorContext(r.ctx, "bus: ack failed", "stream", stream, "group", group, "err", err)
	}
}

// Ping reports whether the underlying Redis connection is reachable.
func (r *Redis) Ping() error {
	return r.client.Do(r.ctx, r.client.B().Ping().Build()).Error()
}

// Close stops all consumers, waits for in-flight batches to finish, then
// closes the underlying rueidis client (the bus owns it).
func (r *Redis) Close() error {
	r.cancel()
	r.wg.Wait()
	r.client.Close()
	return nil
}

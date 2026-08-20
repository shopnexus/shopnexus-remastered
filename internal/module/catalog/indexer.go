package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/provider/embedding"
)

// IndexerConfig is what one pass is allowed to do.
type IndexerConfig struct {
	// Batch is how many rows go to the model in one call. It bounds three things at once: the
	// request, the transaction that writes the answers, and how much work a crash repeats.
	Batch int
	// MaxTextChars clips what the model reads, and it is what a pass costs: inference time is
	// roughly linear in the text. Characters and not tokens, because the tokenizer lives on the
	// other side of the seam. The sparse half's thousand-non-zero cap is enforced by the
	// adapter, so this is a budget rather than a bound — the composed text puts the description
	// last, and that is what falls off.
	MaxTextChars int
}

// Indexer drains catalog's three stale queues into their vector tables.
//
// The mark is the queue: a write that changes what a row *says* sets `embedding_stale_at`,
// and this is the only thing that clears it. That makes the work list a property of the data
// rather than a message anybody has to not lose — a listing edited during a deploy is still
// stale afterwards, and re-running a pass that already ran finds nothing.
type Indexer struct {
	repo   port.Embeddings
	client embedding.Client
	cfg    IndexerConfig
	log    *slog.Logger
}

func NewIndexer(repo port.Embeddings, client embedding.Client, cfg IndexerConfig, log *slog.Logger) (*Indexer, error) {
	if cfg.Batch <= 0 {
		return nil, fmt.Errorf("indexer: batch must be positive, got %d", cfg.Batch)
	}
	if cfg.MaxTextChars <= 0 {
		return nil, fmt.Errorf("indexer: max text chars must be positive, got %d", cfg.MaxTextChars)
	}
	return &Indexer{repo: repo, client: client, cfg: cfg, log: log}, nil
}

// Pass drains every queue until each is empty, and reports what it wrote.
//
// One kind failing does not stop the others: a malformed listing must not keep the category
// tree unembedded. The error carries the first failure so a caller — the `-once` backfill —
// can still exit non-zero.
func (i *Indexer) Pass(ctx context.Context) (int, error) {
	var (
		total int
		first error
	)
	for _, kind := range port.Kinds {
		n, err := i.drain(ctx, kind)
		total += n
		if err != nil && first == nil {
			first = err
		}
		if err != nil {
			i.log.Error("embedding pass", "kind", kind, "embedded", n, "err", err)
		}
	}
	return total, first
}

// drain works one kind a batch at a time until a read comes back empty. Bounded by the queue
// emptying and not by a batch count: a backlog is worked off in one pass rather than one batch
// per interval, which at a minute a batch would never catch up with an import.
//
// Empty and not short: ListStale claims its rows with SKIP LOCKED, so a read is short whenever
// another worker holds the rest of the batch, and treating that as the end of the queue would
// leave a backlog being drained a few rows a minute. The cost is one extra empty read per kind
// per pass, which is an index scan that finds nothing.
func (i *Indexer) drain(ctx context.Context, kind port.Kind) (int, error) {
	embedded := 0
	for {
		stale, err := i.repo.ListStale(ctx, kind, i.cfg.Batch)
		if err != nil {
			return embedded, err
		}
		if len(stale) == 0 {
			return embedded, nil
		}
		n, err := i.embedBatch(ctx, stale)
		embedded += n
		if err != nil {
			return embedded, err
		}
	}
}

func (i *Indexer) embedBatch(ctx context.Context, stale []port.Stale) (int, error) {
	texts := make([]string, len(stale))
	for n, s := range stale {
		texts[n] = clip(s.Text, i.cfg.MaxTextChars)
	}
	vectors, err := i.client.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed %d texts: %w", len(texts), err)
	}
	if len(vectors) != len(stale) {
		return 0, fmt.Errorf("embed returned %d vectors for %d rows", len(vectors), len(stale))
	}
	done := make([]port.Embedded, len(stale))
	for n, s := range stale {
		done[n] = port.Embedded{Stale: s, Dense: vectors[n].Dense, Sparse: vectors[n].Sparse}
	}
	if err := i.repo.Save(ctx, done); err != nil {
		return 0, fmt.Errorf("save %d embeddings: %w", len(done), err)
	}
	return len(done), nil
}

// Run drains on a ticker until the context is cancelled. The first pass is immediate: a worker
// that has just been started is usually being started *because* there is a backlog.
func (i *Indexer) Run(ctx context.Context, interval time.Duration) {
	for {
		started := time.Now()
		n, err := i.Pass(ctx)
		if ctx.Err() != nil {
			return
		}
		// One line per pass, and only when it did something. A queue that is empty — which is
		// the steady state — says nothing, or the log is a heartbeat nobody can read past.
		if n > 0 {
			i.log.Info("embedded", "rows", n, "took", time.Since(started).Round(time.Millisecond))
		}
		if err != nil {
			// Already logged per kind in Pass; the retry is the next tick, because nothing
			// here is lost — the marks are still set.
			i.log.Warn("embedding pass finished with errors", "retry_in", interval)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// clip cuts the text to n characters on a rune boundary. Crude on purpose: it is a guard
// against an unbounded description, not a summariser, and the columns are ordered so that
// what falls off the end is the least searchable part of the row.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	out := []rune(s)
	if len(out) <= n {
		return s
	}
	return string(out[:n])
}

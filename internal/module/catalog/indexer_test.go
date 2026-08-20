package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/provider/embedding"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeEmbeddings is one queue per kind, drained the way the real adapter drains it: oldest first,
// and a row only leaves when Save says so.
type fakeEmbeddings struct {
	queues map[port.Kind][]port.Stale
	saved  []port.Embedded
	// saveErr fails the write, so a test can prove nothing left the queue.
	saveErr error
	listErr map[port.Kind]error
	calls   int
	// contended caps what a read answers regardless of the limit asked for, which is what
	// SKIP LOCKED does when another worker is holding the rest of the batch.
	contended int
}

func newFakeEmbeddings() *fakeEmbeddings {
	return &fakeEmbeddings{queues: map[port.Kind][]port.Stale{}, listErr: map[port.Kind]error{}}
}

func (f *fakeEmbeddings) ListStale(_ context.Context, kind port.Kind, limit int) ([]port.Stale, error) {
	f.calls++
	if err := f.listErr[kind]; err != nil {
		return nil, err
	}
	q := f.queues[kind]
	if f.contended > 0 && limit > f.contended {
		limit = f.contended
	}
	if len(q) > limit {
		q = q[:limit]
	}
	return append([]port.Stale(nil), q...), nil
}

func (f *fakeEmbeddings) Save(_ context.Context, done []port.Embedded) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, done...)
	for _, d := range done {
		rest := f.queues[d.Kind][:0]
		for _, s := range f.queues[d.Kind] {
			if s.ID != d.ID || s.Slug != d.Slug {
				rest = append(rest, s)
			}
		}
		f.queues[d.Kind] = rest
	}
	return nil
}

// fakeClient records what it was asked to embed and answers a vector per text.
type fakeClient struct {
	seen []string
	err  error
	// short makes it answer with one vector fewer than asked, which is the failure that would
	// otherwise pair a row with somebody else's vector.
	short bool
}

func (c *fakeClient) Name() string { return "fake" }

func (c *fakeClient) Embed(_ context.Context, texts []string) ([]embedding.Vector, error) {
	if c.err != nil {
		return nil, c.err
	}
	c.seen = append(c.seen, texts...)
	n := len(texts)
	if c.short {
		n--
	}
	out := make([]embedding.Vector, n)
	for i := range out {
		out[i] = embedding.Vector{Dense: []float32{1, 0}, Sparse: map[uint32]float32{7: 1}}
	}
	return out, nil
}

func newIndexer(t *testing.T, repo port.Embeddings, client embedding.Client, batch, maxChars int) *catalog.Indexer {
	t.Helper()
	idx, err := catalog.NewIndexer(repo, client, catalog.IndexerConfig{Batch: batch, MaxTextChars: maxChars}, discard())
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	return idx
}

func stale(kind port.Kind, id int64, text string) port.Stale {
	return port.Stale{Kind: kind, ID: id, Text: text, StaleAt: time.Now()}
}

// A pass drains every queue to empty, not one batch of each: at one batch per interval a
// backlog from an import would never be caught up with.
func TestPassDrainsEveryQueue(t *testing.T) {
	repo := newFakeEmbeddings()
	for i := int64(1); i <= 5; i++ {
		repo.queues[port.KindListing] = append(repo.queues[port.KindListing], stale(port.KindListing, i, "listing"))
	}
	repo.queues[port.KindCategory] = []port.Stale{stale(port.KindCategory, 1, "category")}
	repo.queues[port.KindTag] = []port.Stale{{Kind: port.KindTag, Slug: "eco", Text: "eco", StaleAt: time.Now()}}

	n, err := newIndexer(t, repo, &fakeClient{}, 2, 100).Pass(context.Background())
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if n != 7 {
		t.Errorf("embedded %d, want all 7 rows", n)
	}
	for kind, q := range repo.queues {
		if len(q) != 0 {
			t.Errorf("%s queue still holds %d rows", kind, len(q))
		}
	}
}

// The mark read is the mark written back, because the adapter clears by comparing them: a row
// edited while the model was working carries a newer one and has to stay queued.
func TestPassCarriesTheStaleMarkThrough(t *testing.T) {
	repo := newFakeEmbeddings()
	mark := time.Now().Add(-time.Hour)
	repo.queues[port.KindListing] = []port.Stale{{Kind: port.KindListing, ID: 42, Text: "a", StaleAt: mark}}

	if _, err := newIndexer(t, repo, &fakeClient{}, 10, 100).Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved %d rows, want 1", len(repo.saved))
	}
	if !repo.saved[0].StaleAt.Equal(mark) {
		t.Errorf("StaleAt = %v, want the mark that was read (%v)", repo.saved[0].StaleAt, mark)
	}
}

// One kind failing must not hold up the others — a malformed listing cannot be allowed to keep
// the category tree unembedded — but the pass still has to report that it failed.
func TestPassKeepsGoingAfterOneKindFails(t *testing.T) {
	repo := newFakeEmbeddings()
	repo.listErr[port.KindListing] = errors.New("boom")
	repo.queues[port.KindCategory] = []port.Stale{stale(port.KindCategory, 1, "category")}
	repo.queues[port.KindTag] = []port.Stale{{Kind: port.KindTag, Slug: "eco", Text: "eco", StaleAt: time.Now()}}

	n, err := newIndexer(t, repo, &fakeClient{}, 10, 100).Pass(context.Background())
	if err == nil {
		t.Error("Pass returned no error, want the listing failure reported")
	}
	if n != 2 {
		t.Errorf("embedded %d, want the other two kinds done anyway", n)
	}
}

// A model that answers with fewer vectors than texts would pair a row with another row's
// vector. Nothing may be written from that batch.
func TestShortAnswerWritesNothing(t *testing.T) {
	repo := newFakeEmbeddings()
	repo.queues[port.KindListing] = []port.Stale{
		stale(port.KindListing, 1, "a"), stale(port.KindListing, 2, "b"),
	}
	_, err := newIndexer(t, repo, &fakeClient{short: true}, 10, 100).Pass(context.Background())
	if err == nil {
		t.Fatal("Pass succeeded on a short answer, want it refused")
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved %d rows from a short answer", len(repo.saved))
	}
}

// A failed write leaves the mark set, which is what makes the next pass a retry rather than a
// row silently losing its vector.
func TestFailedSaveLeavesTheQueueIntact(t *testing.T) {
	repo := newFakeEmbeddings()
	repo.saveErr = errors.New("db down")
	repo.queues[port.KindListing] = []port.Stale{stale(port.KindListing, 1, "a")}

	n, err := newIndexer(t, repo, &fakeClient{}, 10, 100).Pass(context.Background())
	if err == nil {
		t.Fatal("Pass succeeded with a failing Save")
	}
	if n != 0 {
		t.Errorf("counted %d embedded, want none — nothing was written", n)
	}
	if len(repo.queues[port.KindListing]) != 1 {
		t.Error("the row left the queue despite the write failing")
	}
}

// The sparse half has one non-zero per distinct token and the HNSW index refuses more than a
// thousand, so the text the model reads is clipped before it gets there.
func TestTextIsClippedBeforeTheModelSeesIt(t *testing.T) {
	repo := newFakeEmbeddings()
	repo.queues[port.KindListing] = []port.Stale{stale(port.KindListing, 1, strings.Repeat("é", 500))}

	client := &fakeClient{}
	if _, err := newIndexer(t, repo, client, 10, 100).Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(client.seen) != 1 {
		t.Fatalf("model saw %d texts, want 1", len(client.seen))
	}
	// Runes, not bytes: clipping a multi-byte character in half would hand the model a broken
	// string, and every one of these is two bytes.
	if got := []rune(client.seen[0]); len(got) != 100 {
		t.Errorf("model saw %d runes, want it clipped to 100", len(got))
	}
}

func TestNewIndexerRejectsUnusableConfig(t *testing.T) {
	for _, cfg := range []catalog.IndexerConfig{
		{Batch: 0, MaxTextChars: 10},
		{Batch: 10, MaxTextChars: 0},
	} {
		if _, err := catalog.NewIndexer(newFakeEmbeddings(), &fakeClient{}, cfg, discard()); err == nil {
			t.Errorf("NewIndexer(%+v) succeeded, want it refused", cfg)
		}
	}
}

// A batch that comes back short is not a queue that is empty. ListStale claims its rows with
// FOR UPDATE SKIP LOCKED, so a second worker holding the next few makes the read short while
// the backlog is untouched — and a drain that read that as "done" would hand the rest to the
// next tick, a minute later, one short batch at a time.
func TestDrainKeepsGoingAfterAShortRead(t *testing.T) {
	repo := newFakeEmbeddings()
	for i := int64(1); i <= 9; i++ {
		repo.queues[port.KindListing] = append(repo.queues[port.KindListing], stale(port.KindListing, i, "listing"))
	}
	repo.contended = 2 // every read answers 2 of the 8 asked for

	n, err := newIndexer(t, repo, &fakeClient{}, 8, 100).Pass(context.Background())
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if n != 9 {
		t.Errorf("embedded %d, want all 9 rows drained despite every read being short", n)
	}
	if len(repo.queues[port.KindListing]) != 0 {
		t.Errorf("listing queue still holds %d rows", len(repo.queues[port.KindListing]))
	}
}

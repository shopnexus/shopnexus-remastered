//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/port"
)

// staleAges the rows a claim test owns far enough into the past that they sort to the front of
// whatever else the shared schema is holding. Ordering by the mark is what makes a bounded read
// deterministic, so a test that did not control it would read another test's leftovers.
func markStale(t *testing.T, table, key string, id any, ago time.Duration) {
	t.Helper()
	pool := poolOf(t)
	q := `UPDATE ` + table + ` SET embedding_stale_at = @at WHERE ` + key + ` = @id`
	if _, err := pool.Exec(context.Background(), q,
		pgx.NamedArgs{"at": time.Now().Add(-ago), "id": id}); err != nil {
		t.Fatalf("mark %s %v stale: %v", table, id, err)
	}
}

func newEmbeddings(t *testing.T) *pgadapter.Embeddings {
	t.Helper()
	return pgadapter.NewEmbeddings(poolOf(t))
}

// denseVector is a full-width vector, because the column is vector(1024) and a short one is
// refused at the write.
func denseVector() []float32 {
	v := make([]float32, 1024)
	v[0] = 1
	return v
}

func staleIDs(stale []port.Stale) []int64 {
	out := make([]int64, len(stale))
	for i, s := range stale {
		out[i] = s.ID
	}
	return out
}

// A read claims what it answers, so the next bounded read steps past it. Without that, two
// embedder processes read the same head of the queue and buy the same vectors twice — and a lock
// cannot do it, because the model call that follows outlives the statement that took it.
func TestEmbeddings_ListStaleClaimsWhatItReads(t *testing.T) {
	repo := newRepo(t)
	e := newEmbeddings(t)
	ctx := context.Background()

	mine := make([]int64, 0, 6)
	for i := range 6 {
		c := createCategory(t, repo, unique("claim-"), nil)
		// Staggered and ancient, so these six are the front of the queue in a known order.
		markStale(t, "category", "id", c.ID, time.Duration(1000-i)*time.Hour)
		mine = append(mine, c.ID)
	}

	first, err := e.ListStale(ctx, port.KindCategory, 3)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	second, err := e.ListStale(ctx, port.KindCategory, 3)
	if err != nil {
		t.Fatalf("ListStale again: %v", err)
	}

	a, b := staleIDs(first), staleIDs(second)
	// At most the limit, and never the same row twice: a batch is allowed to come back short
	// (that is what SKIP LOCKED does when somebody else holds the rest), so its length is not
	// what this asserts. That the two reads do not overlap is.
	if len(a) > 3 || len(b) > 3 {
		t.Fatalf("read %d then %d rows, want at most 3 each", len(a), len(b))
	}
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("read %d then %d rows, want the six ancient categories to be found", len(a), len(b))
	}
	for _, id := range a {
		if slices.Contains(b, id) {
			t.Errorf("category %d came back from both reads; the first read did not claim it", id)
		}
	}
	for _, id := range append(slices.Clone(a), b...) {
		if !slices.Contains(mine, id) {
			t.Errorf("read category %d, which is older than the six this test made stale", id)
		}
	}
}

// The claim is what lets a backfill be split across processes, so this is the property stated as
// the thing it has to survive: many readers at once, and no listing embedded twice. It also pins
// the limit — a batch bounds the model call and the write transaction, and a read that answered
// more than it was asked for would blow both.
func TestEmbeddings_ConcurrentReadersNeverClaimTheSameRow(t *testing.T) {
	e := newEmbeddings(t)
	ctx := context.Background()
	pool := poolOf(t)

	// Raw inserts and one delete: 200 rows through the aggregate would be 200 round trips of
	// setup for a test about what happens after they exist. Ancient, so they are the front of
	// the queue whatever else the shared schema holds.
	const seeded = 200
	tag := unique("race-")
	if _, err := pool.Exec(ctx, `INSERT INTO category (name, description, embedding_stale_at)
	    SELECT @tag || g, '', now() - (5000 - g) * interval '1 minute'
	    FROM generate_series(1, @n) g`, pgx.NamedArgs{"tag": tag, "n": seeded}); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM category WHERE name LIKE @like`,
			pgx.NamedArgs{"like": tag + "%"}); err != nil {
			t.Logf("cleanup categories %s: %v", tag, err)
		}
	})

	const (
		readers = 4
		reads   = 10
		limit   = 3
	)
	// readers*reads*limit is 120, comfortably under the 200 seeded, so every claim in this test
	// is a first claim — a row only comes round again once the queue has been walked.
	var mu sync.Mutex
	seen := map[int64]int{}
	overLimit := 0

	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range reads {
				batch, err := e.ListStale(ctx, port.KindCategory, limit)
				mu.Lock()
				if err != nil {
					t.Errorf("ListStale: %v", err)
				}
				if len(batch) > limit {
					overLimit++
				}
				for _, s := range batch {
					seen[s.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if overLimit > 0 {
		t.Errorf("%d reads answered more rows than the limit they were given", overLimit)
	}
	for id, times := range seen {
		if times > 1 {
			t.Errorf("category %d was claimed %d times; two workers embedded the same row", id, times)
		}
	}
	if len(seen) == 0 {
		t.Error("no rows were claimed at all")
	}
}

// The mark handed back is the claim, not the mark the row went stale with — Save clears by
// comparing against it, so the two have to be the same value.
func TestEmbeddings_ListStaleReturnsTheClaimAndSaveClearsIt(t *testing.T) {
	repo := newRepo(t)
	e := newEmbeddings(t)
	ctx := context.Background()

	c := createCategory(t, repo, unique("claim-save-"), nil)
	original := time.Now().Add(-999 * time.Hour)
	markStale(t, "category", "id", c.ID, 999*time.Hour)

	stale, err := e.ListStale(ctx, port.KindCategory, 1)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != c.ID {
		t.Fatalf("read %+v, want the one ancient category %d", staleIDs(stale), c.ID)
	}
	if !stale[0].StaleAt.After(original) {
		t.Errorf("StaleAt = %v, want the claim rather than the original mark %v", stale[0].StaleAt, original)
	}

	if err := e.Save(ctx, []port.Embedded{{
		Stale: stale[0], Dense: denseVector(), Sparse: map[uint32]float32{7: 1},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	pool := poolOf(t)
	var mark *time.Time
	if err := pool.QueryRow(ctx, `SELECT embedding_stale_at FROM category WHERE id = @id`,
		pgx.NamedArgs{"id": c.ID}).Scan(&mark); err != nil {
		t.Fatalf("read mark back: %v", err)
	}
	if mark != nil {
		t.Errorf("mark = %v, want it cleared by the save that carried the claim", *mark)
	}
	var written int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM category_embedding WHERE category_id = @id AND dense IS NOT NULL`,
		pgx.NamedArgs{"id": c.ID}).Scan(&written); err != nil {
		t.Fatalf("read vector back: %v", err)
	}
	if written != 1 {
		t.Errorf("wrote %d vectors, want 1", written)
	}
}

// Claiming must not weaken the guard it reuses: a row edited while the model was working carries
// a mark the save does not match, so it keeps its place in the queue — and still gets the vector,
// because a slightly stale one beats none while it waits.
func TestEmbeddings_SaveLeavesARowEditedMidFlightQueued(t *testing.T) {
	repo := newRepo(t)
	e := newEmbeddings(t)
	ctx := context.Background()

	c := createCategory(t, repo, unique("claim-raced-"), nil)
	markStale(t, "category", "id", c.ID, 998*time.Hour)

	stale, err := e.ListStale(ctx, port.KindCategory, 1)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != c.ID {
		t.Fatalf("read %+v, want category %d", staleIDs(stale), c.ID)
	}
	// The seller edits it while the model is working.
	markStale(t, "category", "id", c.ID, 0)

	if err := e.Save(ctx, []port.Embedded{{
		Stale: stale[0], Dense: denseVector(), Sparse: map[uint32]float32{7: 1},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	pool := poolOf(t)
	var mark *time.Time
	if err := pool.QueryRow(ctx, `SELECT embedding_stale_at FROM category WHERE id = @id`,
		pgx.NamedArgs{"id": c.ID}).Scan(&mark); err != nil {
		t.Fatalf("read mark back: %v", err)
	}
	if mark == nil {
		t.Error("the mark was cleared, so the edit made during the pass is now unembedded for good")
	}
}

// The listing text is composed in RETURNING, where the join the query used to have is not
// available — so the category, the tags and the specification values come from subqueries, and
// this is what proves they still land in the text the model reads.
func TestEmbeddings_ListStaleComposesTheListingText(t *testing.T) {
	repo := newRepo(t)
	e := newEmbeddings(t)
	ctx := context.Background()
	pool := poolOf(t)

	c := createCategory(t, repo, unique("Thời trang "), nil)
	createTag(t, repo, slugOf("zz-the-thao-"), nil)
	createTag(t, repo, slugOf("aa-ao-thun-"), nil)

	var tags []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(id ORDER BY id) FROM tag WHERE id LIKE 'zz-the-thao-%' OR id LIKE 'aa-ao-thun-%'`).
		Scan(&tags); err != nil {
		t.Fatalf("read tags back: %v", err)
	}

	const insert = `INSERT INTO listing
	    (slug, account_id, category_id, name, description, specifications,
	     price_mode, condition, currency, embedding_stale_at)
	    VALUES (@slug, 1, @category_id, @name, @description, @specifications::jsonb,
	            'fixed', 'new', 'VND', @stale_at)
	    RETURNING id`
	var listingID int64
	if err := pool.QueryRow(ctx, insert, pgx.NamedArgs{
		"slug":           unique("compose-"),
		"category_id":    c.ID,
		"name":           "Áo thun thể thao nam",
		"description":    "Cotton thoáng khí",
		"specifications": `{"Chất liệu": "Cotton", "Xuất xứ": "Trung Quốc"}`,
		"stale_at":       time.Now().Add(-997 * time.Hour),
	}).Scan(&listingID); err != nil {
		t.Fatalf("insert listing: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM listing WHERE id = @id`,
			pgx.NamedArgs{"id": listingID}); err != nil {
			t.Logf("cleanup listing %d: %v", listingID, err)
		}
	})
	for _, tag := range tags {
		if _, err := pool.Exec(ctx, `INSERT INTO listing_tag (listing_id, tag) VALUES (@id, @tag)`,
			pgx.NamedArgs{"id": listingID, "tag": tag}); err != nil {
			t.Fatalf("link tag %q: %v", tag, err)
		}
	}

	stale, err := e.ListStale(ctx, port.KindListing, 1)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != listingID {
		t.Fatalf("read %+v, want the one ancient listing %d", staleIDs(stale), listingID)
	}

	text := stale[0].Text
	for _, want := range []string{"Áo thun thể thao nam", c.Name, "Cotton", "Trung Quốc", "Cotton thoáng khí"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q is missing %q", text, want)
		}
	}
	// Tags are aggregated in sorted order, which is what lets the domain treat a reordered tag
	// list as no change at all.
	sorted := slices.Clone(tags)
	slices.Sort(sorted)
	if !strings.Contains(text, strings.Join(sorted, " ")) {
		t.Errorf("text %q does not carry the tags in sorted order (%v)", text, sorted)
	}
}

//go:build integration

package uploads_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/provider/storage"
	"shopnexus/internal/provider/storage/local"
	"shopnexus/internal/provider/storage/remote"
)

// newStore builds the real thing: one module's `resource` table plus a filesystem store. Any
// module's schema would do — `common/migrations` puts the same table in every one — and trust's
// is used because a stray review photo is the least entangled row to leave behind.
func newStore(t *testing.T, slotTTL time.Duration) (*uploads.Store, *dbx.Resources, *local.Client) {
	t.Helper()
	dsn := os.Getenv("TRUST_DB_DSN")
	if dsn == "" {
		t.Skip("TRUST_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "trust")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	client, err := local.New(local.Config{
		Root:         t.TempDir(),
		BaseURL:      "http://127.0.0.1:5000/api/v1",
		Secret:       "0123456789abcdef0123456789abcdef",
		UploadTTL:    slotTTL,
		DownloadTTL:  time.Hour,
		MaxSize:      1 << 20,
		MaxVideoSize: 8 << 20,
		AllowedMimes: []string{"image/jpeg"},
	})
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	// The same registry the gateway builds: local writes, remote is readable beside it.
	stores, err := storage.NewRegistry(client, remote.New())
	if err != nil {
		t.Fatalf("storage registry: %v", err)
	}
	resources := dbx.NewResources(pool)
	return uploads.New(resources, stores, "trust", slotTTL), resources, client
}

func uploaderID() int64 { return time.Now().UnixNano() % 1_000_000_000 }

// The reaper's whole contract: a slot nobody confirmed loses both its row and its bytes, a
// confirmed upload is untouched, and a slot still inside its window is not taken from a client
// who is mid-upload.
func TestReap_TakesTheAbandonedAndNothingElse(t *testing.T) {
	store, resources, client := newStore(t, time.Minute)
	ctx := context.Background()
	who := uploaderID()

	abandoned, err := store.Presign(ctx, who, "review", common.UploadRequest{
		Filename: "walked-away.jpg", Mime: "image/jpeg", Size: 4,
	})
	if err != nil {
		t.Fatalf("Presign abandoned: %v", err)
	}
	confirmedSlot, err := store.Presign(ctx, who, "review", common.UploadRequest{
		Filename: "arrived.jpg", Mime: "image/jpeg", Size: 4,
	})
	if err != nil {
		t.Fatalf("Presign confirmed: %v", err)
	}
	fresh, err := store.Presign(ctx, who, "review", common.UploadRequest{
		Filename: "still-going.jpg", Mime: "image/jpeg", Size: 4,
	})
	if err != nil {
		t.Fatalf("Presign fresh: %v", err)
	}

	// Two of the three sent their bytes; only one of those confirmed.
	abandonedKey := objectKey(t, resources, abandoned.ResourceID, who)
	confirmedKey := objectKey(t, resources, confirmedSlot.ResourceID, who)
	for _, key := range []string{abandonedKey, confirmedKey} {
		if _, err := client.Write(key, strings.NewReader("data")); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	if _, err := store.Confirm(ctx, who, confirmedSlot.ResourceID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// A window of zero makes every unconfirmed row old enough — except that `fresh` is one of
	// them, so the pass is run twice: first with a window nothing has outlived, then with none.
	reaped, err := store.Reap(ctx, time.Hour, 100)
	if err != nil {
		t.Fatalf("Reap inside the window: %v", err)
	}
	if reaped != 0 {
		t.Fatalf("reaped = %d, want a client mid-upload left alone", reaped)
	}

	if _, err := store.Reap(ctx, 0, 100); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	// Only what this test created is asserted on: the pass is shared, so another run's rows may
	// have gone with it.
	if _, err := resources.FindPending(ctx, abandoned.ResourceID, &who); !errors.Is(err, common.ErrResourceNotFound) {
		t.Fatalf("FindPending abandoned = %v, want it gone", err)
	}
	if _, _, err := client.Open(abandonedKey); err == nil {
		t.Error("the bytes of an abandoned upload survived the row")
	}
	if _, err := resources.FindPending(ctx, fresh.ResourceID, &who); !errors.Is(err, common.ErrResourceNotFound) {
		t.Fatalf("FindPending fresh = %v, want it reaped by the zero window", err)
	}

	// The confirmed one is a real resource and is not the reaper's business: it is no longer a
	// slot, which is the whole distinction the `Abandoned` query rests on.
	found, err := resources.Find(ctx, []int64{confirmedSlot.ResourceID})
	if err != nil {
		t.Fatalf("Find confirmed: %v", err)
	}
	if len(found) != 1 || found[0].Size != 4 {
		t.Fatalf("confirmed = %+v, want the row with the store's own size", found)
	}
	if _, _, err := client.Open(confirmedKey); err != nil {
		t.Errorf("the bytes of a confirmed upload were removed: %v", err)
	}
}

// Reap works in batches, so a backlog is several passes rather than one transaction nobody can
// see the end of.
func TestReap_StopsAtTheBatchLimit(t *testing.T) {
	store, resources, client := newStore(t, time.Minute)
	ctx := context.Background()
	who := uploaderID()

	// Drain first: the limit is only observable against a known number of abandoned rows, and
	// another run's leftovers would be eaten by this batch instead of the three below.
	if _, err := store.Reap(ctx, 0, 1000); err != nil {
		t.Fatalf("drain: %v", err)
	}
	slots := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		slot, err := store.Presign(ctx, who, "review", common.UploadRequest{
			Filename: "abandoned.jpg", Mime: "image/jpeg", Size: 4,
		})
		if err != nil {
			t.Fatalf("Presign %d: %v", i, err)
		}
		if _, err := client.Write(objectKey(t, resources, slot.ResourceID, who), strings.NewReader("data")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		slots = append(slots, slot.ResourceID)
	}

	reaped, err := store.Reap(ctx, 0, 2)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if reaped != 2 {
		t.Fatalf("reaped = %d, want the batch to stop at its limit", reaped)
	}
	left := 0
	for _, id := range slots {
		if _, err := resources.FindPending(ctx, id, &who); err == nil {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("%d slots left, want the third waiting for the next pass", left)
	}
}

// Resolve has to reach the store each row names, not the one this deployment happens to write
// to. Resolving everything through the write store does not fail loudly — a store signs any key
// it is handed — so the wrong link is well-formed and only dies in the browser. This is that
// case: two rows, two stores, one call.
func TestResolveUsesTheStoreEachRowNames(t *testing.T) {
	store, resources, client := newStore(t, time.Minute)
	ctx := context.Background()
	who := uploaderID()

	// One upload through the write store, confirmed the ordinary way.
	slot, err := store.Presign(ctx, who, "review", common.UploadRequest{
		Filename: "mine.jpg", Mime: "image/jpeg", Size: 4,
	})
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	if _, err := client.Write(objectKey(t, resources, slot.ResourceID, who), strings.NewReader("data")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Confirm(ctx, who, slot.ResourceID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// One imported row, held at another origin — what cmd/seed writes. Inserted directly
	// because no route can create one: an upload always goes to the write store. The key is unique
	// per run, since `resource` has one row per (provider, object_key) and these rows outlive a
	// test — a fixed key passes once and then collides for ever.
	cdnURL := fmt.Sprintf("https://cdn.example.com/file/imported_%d", who)
	imported, err := common.NewResource(&who, remote.Name, cdnURL, "image/jpeg", 0, nil, nil)
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	if err := resources.Insert(ctx, &imported); err != nil {
		t.Fatalf("insert imported: %v", err)
	}
	if _, err := resources.Confirm(ctx, imported.ID, &who, 0, nil); err != nil {
		t.Fatalf("confirm imported: %v", err)
	}

	got, err := store.Resolve(ctx, []int64{slot.ResourceID, imported.ID})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	mine, ok := got[slot.ResourceID]
	if !ok {
		t.Fatal("the uploaded resource did not resolve")
	}
	if !strings.HasPrefix(mine.URL, "http://127.0.0.1:5000/api/v1") {
		t.Errorf("uploaded url = %q, want a link signed by the local store", mine.URL)
	}
	if mine.URLExpiresAt == nil {
		t.Error("a signed link with no expiry: the caller cannot tell a stale URL from a wrong one")
	}

	other, ok := got[imported.ID]
	if !ok {
		t.Fatal("the imported resource did not resolve")
	}
	if other.URL != cdnURL {
		t.Errorf("imported url = %q, want the origin's own %q", other.URL, cdnURL)
	}
	// Before the registry this was a local signature over a CDN URL: a link that verified and
	// then answered 409, with an expiry this platform had invented for somebody else's object.
	if other.URLExpiresAt != nil {
		t.Errorf("imported url_expires_at = %v, want none on a link this platform does not sign", *other.URLExpiresAt)
	}
}

func objectKey(t *testing.T, resources *dbx.Resources, id, uploaderID int64) string {
	t.Helper()
	res, err := resources.FindPending(context.Background(), id, &uploaderID)
	if err != nil {
		t.Fatalf("find pending %d: %v", id, err)
	}
	return res.ObjectKey
}

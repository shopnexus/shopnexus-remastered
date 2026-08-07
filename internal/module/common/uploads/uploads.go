// Package uploads is the two-step upload every module that takes a file performs, written once.
//
// Step one mints a `resource` row and a presigned slot; the client PUTs the bytes at the store;
// step two confirms the row, taking the size from the *store* rather than from the client that
// claims it. Until that confirm lands the row is invisible to `Find`, which is why a listing can
// never render a photo whose bytes never arrived.
//
// It lives under `common` rather than in `shared` because it needs `common.Resource` and the
// per-schema `dbx.Resources` store — and in its own package rather than in `common` itself, so
// that a `port` or an `api` package can still name `common.Resource` without pulling pgx and a
// storage client behind it.
package uploads

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/provider/storage"
)

// Store is one module's uploads: its own `resource` table, the stores its objects live in, and
// the key prefix under which it writes. A module holds one, so an upload belongs to the module
// that took it and travels with that module if it moves to its own database.
//
// `stores` is a registry and not a single client because a row records which store holds it:
// new uploads go to the preferred one, while a read reaches whichever store the row names. See
// storage.Registry for why the two cannot be the same question.
type Store struct {
	resources *dbx.Resources
	stores    *storage.Registry
	prefix    string
	// slotTTL is how long a slot stays writable. Past it the row is dead — nothing can write
	// to the URL any more — which is what makes it safe for the reaper to take.
	slotTTL time.Duration
}

func New(resources *dbx.Resources, stores *storage.Registry, prefix string, slotTTL time.Duration) *Store {
	return &Store{resources: resources, stores: stores, prefix: prefix, slotTTL: slotTTL}
}

var _ common.Uploads = (*Store)(nil)

// Presign reserves a row and a slot. The row comes first: a slot with no row behind it is an
// object nobody can confirm or reap, and the row is invisible until it is confirmed anyway.
//
// `kind` narrows the prefix — "listing", "review", "receipt" — so an operator holding only a key
// can tell what it belongs to.
func (s *Store) Presign(ctx context.Context, uploaderID int64, kind string, req common.UploadRequest) (common.UploadSlot, error) {
	// The preferred store, and its name goes on the row: that is what lets a later deployment
	// that has moved on to another store still serve this object.
	write := s.stores.Write()
	upload, err := write.PresignUpload(ctx, storage.NewUpload{
		Prefix:   s.prefix + "/" + kind,
		Filename: req.Filename,
		Mime:     req.Mime,
		Size:     req.Size,
	})
	if err != nil {
		return common.UploadSlot{}, err
	}
	res, err := common.NewResource(uploader(uploaderID), write.Name(), upload.ObjectKey,
		req.Mime, req.Size, nil, nil)
	if err != nil {
		return common.UploadSlot{}, err
	}
	if err := s.resources.Insert(ctx, &res); err != nil {
		return common.UploadSlot{}, fmt.Errorf("reserve upload: %w", err)
	}
	return common.UploadSlot{
		ResourceID: res.ID,
		URL:        upload.URL,
		Headers:    upload.Headers,
		ExpiresAt:  upload.ExpiresAt,
	}, nil
}

// Confirm makes the upload real. The size and checksum come from the store, so a client that
// declared 3 KB and sent 30 MB does not get to set the record — and a confirm for bytes that
// never arrived is refused rather than producing a row that renders as a broken image.
func (s *Store) Confirm(ctx context.Context, uploaderID, resourceID int64) (common.Resource, error) {
	pending, err := s.resources.FindPending(ctx, resourceID, uploader(uploaderID))
	if err != nil {
		return common.Resource{}, err
	}
	// The store the slot was issued against, not today's preferred one: a deployment that
	// switched stores mid-upload must still confirm against the bucket the bytes went to.
	client, err := s.stores.For(pending.Provider)
	if err != nil {
		return common.Resource{}, err
	}
	object, err := client.Stat(ctx, pending.ObjectKey)
	if err != nil {
		return common.Resource{}, err
	}
	var checksum *string
	if object.Checksum != "" {
		checksum = &object.Checksum
	}
	res, err := s.resources.Confirm(ctx, resourceID, uploader(uploaderID), object.Size, checksum)
	if err != nil {
		return common.Resource{}, err
	}
	// The link to what was just uploaded, and this is the only chance to hand one over: a second
	// confirm is refused by the `completed_at IS NULL` guard, and nothing else reads a resource
	// on its own — a row is otherwise seen only through whatever it gets attached to.
	s.sign(ctx, &res)
	return res, nil
}

// sign fills in the short-lived read link, which is not a column on the row: only a store can
// produce one, and only for the object it actually holds.
//
// Signing through the preferred store instead of the one the row names is the one thing this must
// not do: a store signs any key it is handed, so an object it has never held comes back as a
// well-formed link that serves nothing and no error branch fires to say so.
//
// Best-effort — a link that could not be produced is a picture that does not render, not a row the
// caller may not have.
func (s *Store) sign(ctx context.Context, res *common.Resource) {
	client, err := s.stores.For(res.Provider)
	if err != nil {
		return
	}
	url, expires, err := client.PresignDownload(ctx, res.ObjectKey, 0)
	if err != nil {
		return
	}
	res.URL = url
	// A zero expiry means the link has none of this platform's making — a public URL at somebody
	// else's origin. Reporting a deadline we do not set would be inventing one.
	if !expires.IsZero() {
		res.URLExpiresAt = &expires
	}
}

// Resolve turns ids into DTOs with a short-lived read link on each. Presigned rather than
// public: an identity scan and a product photo sit in the same store, and only one of them may
// be world-readable.
func (s *Store) Resolve(ctx context.Context, ids []int64) (map[int64]common.ResourceDTO, error) {
	out := make(map[int64]common.ResourceDTO, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	found, err := s.resources.Find(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("find resources: %w", err)
	}
	for _, res := range found {
		s.sign(ctx, &res)
		out[res.ID] = res.ToDTO()
	}
	return out, nil
}

// Bytes reads the objects behind the ids, in the order asked. Each through the store its own row
// names, for the same reason Resolve is: a store hands back whatever key it is given, so reading
// everything through the preferred one would answer another store's object with silence or, worse,
// with somebody else's bytes.
//
// An id that resolves to nothing is simply absent — the caller asked about photos, and one that was
// never confirmed is not a photo yet. A store that cannot be read at all is an error, because that
// is a request the caller has to change rather than a gap they can render around.
func (s *Store) Bytes(ctx context.Context, ids []int64) ([]common.Blob, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	found, err := s.resources.Find(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("find resources: %w", err)
	}
	byID := make(map[int64]common.Resource, len(found))
	for _, res := range found {
		byID[res.ID] = res
	}
	out := make([]common.Blob, 0, len(ids))
	for _, id := range ids {
		res, ok := byID[id]
		if !ok {
			continue
		}
		client, err := s.stores.For(res.Provider)
		if err != nil {
			return nil, err
		}
		blob, err := client.Fetch(ctx, res.ObjectKey)
		if err != nil {
			return nil, fmt.Errorf("read object %d: %w", id, err)
		}
		mime := blob.Mime
		if mime == "" {
			mime = res.Mime
		}
		out = append(out, common.Blob{ResourceID: id, Mime: mime, Data: blob.Data})
	}
	return out, nil
}

// Sweep is the reaper as a periodic pass, shaped for the shared sweeper. A slot nobody confirmed
// is invisible either way, so this is housekeeping rather than correctness: without it the rows
// and the objects behind them accumulate for every upload a client started and walked away from.
//
// The window is twice the slot's own lifetime, so a client that is still mid-upload when the pass
// runs is never the one reaped.
func (s *Store) Sweep(ctx context.Context, log *slog.Logger) {
	reaped, err := s.Reap(ctx, 2*s.slotTTL, reapBatch)
	if err != nil {
		log.Error("reap abandoned uploads", "prefix", s.prefix, "err", err)
		return
	}
	if reaped > 0 {
		log.Info("swept", "what", "abandoned uploads", "prefix", s.prefix, "count", reaped)
	}
}

// reapBatch bounds one pass, so a backlog is worked over several rather than in one transaction
// nobody can see the end of.
const reapBatch = 200

// Reap removes the slots nobody confirmed: the row first so nothing new can resolve it, then the
// object. A failed object delete leaves a soft-deleted row and a stray file, which is a bucket to
// tidy rather than an image that renders after it was withdrawn.
func (s *Store) Reap(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	abandoned, err := s.resources.Abandoned(ctx, olderThan, limit)
	if err != nil {
		return 0, fmt.Errorf("read abandoned uploads: %w", err)
	}
	reaped := 0
	for _, res := range abandoned {
		if err := s.resources.Delete(ctx, res.ID); err != nil {
			return reaped, err
		}
		client, err := s.stores.For(res.Provider)
		if err != nil {
			// Same as a failed delete below: the row is gone either way.
			continue
		}
		if err := client.Remove(ctx, res.ObjectKey); err != nil {
			// The row is gone, so nothing renders it; the object is a byte to sweep later.
			continue
		}
		reaped++
	}
	return reaped, nil
}

// uploader is the nullable column's Go side: a system-generated resource has no uploader, and
// every upload through this package has one.
func uploader(accountID int64) *int64 {
	if accountID == 0 {
		return nil
	}
	return &accountID
}

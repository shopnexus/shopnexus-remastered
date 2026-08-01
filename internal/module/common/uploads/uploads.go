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

// Store is one module's uploads: its own `resource` table, the object store, and the key prefix
// its objects live under. A module holds one, so an upload belongs to the module that took it
// and travels with that module if it moves to its own database.
type Store struct {
	resources *dbx.Resources
	client    storage.Client
	prefix    string
	// slotTTL is how long a slot stays writable. Past it the row is dead — nothing can write
	// to the URL any more — which is what makes it safe for the reaper to take.
	slotTTL time.Duration
}

func New(resources *dbx.Resources, client storage.Client, prefix string, slotTTL time.Duration) *Store {
	return &Store{resources: resources, client: client, prefix: prefix, slotTTL: slotTTL}
}

var _ common.Uploads = (*Store)(nil)

// Presign reserves a row and a slot. The row comes first: a slot with no row behind it is an
// object nobody can confirm or reap, and the row is invisible until it is confirmed anyway.
//
// `kind` narrows the prefix — "listing", "review", "receipt" — so an operator holding only a key
// can tell what it belongs to.
func (s *Store) Presign(ctx context.Context, uploaderID int64, kind string, req common.UploadRequest) (common.UploadSlot, error) {
	upload, err := s.client.PresignUpload(ctx, storage.NewUpload{
		Prefix:   s.prefix + "/" + kind,
		Filename: req.Filename,
		Mime:     req.Mime,
		Size:     req.Size,
	})
	if err != nil {
		return common.UploadSlot{}, err
	}
	res, err := common.NewResource(uploader(uploaderID), s.client.Name(), upload.ObjectKey,
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
	object, err := s.client.Stat(ctx, pending.ObjectKey)
	if err != nil {
		return common.Resource{}, err
	}
	var checksum *string
	if object.Checksum != "" {
		checksum = &object.Checksum
	}
	return s.resources.Confirm(ctx, resourceID, uploader(uploaderID), object.Size, checksum)
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
		url, expires, err := s.client.PresignDownload(ctx, res.ObjectKey, 0)
		if err != nil {
			// A link that could not be signed is a picture that does not render, not a page
			// that fails: the row is still what the caller asked about.
			out[res.ID] = res.ToDTO()
			continue
		}
		res.URL, res.URLExpiresAt = url, &expires
		out[res.ID] = res.ToDTO()
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
		if err := s.client.Remove(ctx, res.ObjectKey); err != nil {
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

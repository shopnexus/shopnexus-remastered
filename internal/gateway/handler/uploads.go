package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"shopnexus/internal/provider/storage"
	"shopnexus/internal/provider/storage/local"
	"shopnexus/internal/shared/errx"
)

// Uploads serves the object route the `local` storage backend signs its slots against. It
// exists only for that backend: a real store signs its own bucket and the bytes never reach
// this process, which is why this is registered from the provider rather than unconditionally.
//
// No bearer token, deliberately. The signature *is* the authorization — it names the method,
// the key and an expiry, so a slot cannot be turned into a read link, reused for another key, or
// replayed after it lapses. A token here would be a second answer to a question the signature
// has already answered, and it would stop a client from handing the URL to anything but itself.
type Uploads struct {
	client *local.Client
	log    *slog.Logger
}

func NewUploads(client *local.Client, log *slog.Logger) *Uploads {
	return &Uploads{client: client, log: log}
}

// Put stores the bytes of a signed slot. Handles PUT /uploads/object.
func (h *Uploads) Put(w http.ResponseWriter, r *http.Request) {
	key, err := h.verified(r, http.MethodPut)
	if failed(w, h.log, err) {
		return
	}
	written, err := h.client.Write(key, r.Body)
	if failed(w, h.log, err) {
		return
	}
	// The confirm step is what makes the row visible; this only reports what landed, so a
	// client can check it against what it sent before confirming.
	w.Header().Set("Content-Length", "0")
	w.Header().Set("X-Object-Size", strconv.FormatInt(written, 10))
	w.WriteHeader(http.StatusOK)
}

// Get serves an object back for a signed read link. Handles GET /uploads/object.
func (h *Uploads) Get(w http.ResponseWriter, r *http.Request) {
	key, err := h.verified(r, http.MethodGet)
	if failed(w, h.log, err) {
		return
	}
	body, mime, err := h.client.Open(key)
	if failed(w, h.log, err) {
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", mime)
	// Named so a browser cannot be talked into rendering a stored file as a page, and so a
	// signed link is not cached past its own expiry by something in the middle.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=60")
	// A zero modtime: the store keeps none, and the signed URL's own expiry is the freshness
	// contract, so there is nothing for a conditional request to compare against.
	http.ServeContent(w, r, key, time.Time{}, body)
}

// verified is the signature check both routes start with. A bad signature and a missing object
// answer the same way on purpose: telling them apart would say whether a key exists.
func (h *Uploads) verified(r *http.Request, method string) (string, error) {
	q := r.URL.Query()
	key := q.Get("key")
	if err := h.client.Verify(method, key, q.Get("expires"), q.Get("signature")); err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return "", errx.ErrUnauthorized
		}
		return "", err
	}
	return key, nil
}

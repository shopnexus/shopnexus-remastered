// Package local is the storage backend a development stack runs on: objects on disk, and a
// "presigned" URL that points back at the gateway's own upload route rather than at a bucket.
//
// It is the mock of this seam, and it is deliberately not called mock: it stores the bytes and
// serves them back, so a photo uploaded in dev actually renders. What it does not do is take
// the bytes off this process — with a real store the client PUTs straight to the bucket, and
// here it PUTs to us. That is the one difference, and it is why the signature covers the key,
// the method and an expiry: without it the upload route would be an open write to any path.
package local

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/provider/storage"
)

// Config is what a deployment has to decide. No defaults, like every other provider here.
type Config struct {
	// Root is the directory objects live under. One per deployment; a shared one between two
	// deployments would let each serve the other's keys.
	Root string
	// BaseURL is where this gateway answers the upload and download routes, as the client
	// sees it — behind a proxy that is not the address the server binds.
	BaseURL string
	// Secret keys the signature. A separate secret from the JWT's: this one signs a URL that
	// may sit in a client's memory and be replayed, and rotating it invalidates only slots
	// still in flight.
	Secret string
	// UploadTTL is how long a slot stays writable, DownloadTTL how long a read link lives.
	UploadTTL   time.Duration
	DownloadTTL time.Duration
	// MaxSize is the largest object accepted, refused at the presign rather than after the
	// bytes have already been sent.
	MaxSize int64
	// AllowedMimes is what may be stored at all. An allowlist: a store that accepts anything
	// is a store that serves anything back, and `text/html` from your own domain is a stored
	// cross-site script.
	AllowedMimes []string
}

// Client implements storage.Client over the filesystem.
type Client struct {
	cfg Config
}

func New(cfg Config) (*Client, error) {
	if cfg.Root == "" || cfg.BaseURL == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("local storage needs a root, a base URL and a secret")
	}
	if cfg.UploadTTL <= 0 || cfg.DownloadTTL <= 0 || cfg.MaxSize <= 0 {
		return nil, fmt.Errorf("local storage needs a positive upload TTL, download TTL and max size")
	}
	if err := os.MkdirAll(cfg.Root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Client{cfg: cfg}, nil
}

var _ storage.Client = (*Client)(nil)

// Name is the STORAGE_PROVIDER value that selects this backend, and the value written to
// resource.provider.
const Name = "local"

// The route a signed slot points at, and the three query keys that carry the signature. This
// backend builds the URL and the gateway serves it, so the strings are declared once: a renamed
// route on one side is otherwise not a compile error but a 401 on every upload.
const (
	ObjectPath     = "/uploads/object"
	ParamKey       = "key"
	ParamExpires   = "expires"
	ParamSignature = "signature"
)

func (c *Client) Name() string { return Name }

// PresignUpload mints a key and signs a URL to the gateway's upload route. The key is
// generated, never the client's filename: a path a caller chose is a directory traversal
// waiting for a backend that resolves one.
func (c *Client) PresignUpload(ctx context.Context, req storage.NewUpload) (storage.Upload, error) {
	if req.Size > c.cfg.MaxSize {
		return storage.Upload{}, storage.ErrTooLarge
	}
	if !c.allowed(req.Mime) {
		return storage.Upload{}, storage.ErrMimeNotAllowed
	}
	key := objectKey(req.Prefix, req.Filename)
	expires := time.Now().Add(c.cfg.UploadTTL)
	return storage.Upload{
		URL:       c.signedURL("PUT", key, expires),
		ObjectKey: key,
		ExpiresAt: expires,
		// The size is signed into nothing here, so it is enforced by the route reading at
		// most MaxSize bytes. Named in the headers so a client sends what it declared.
		Headers: map[string]string{"Content-Type": req.Mime},
	}, nil
}

// Stat is the confirm step's evidence that the bytes arrived.
func (c *Client) Stat(ctx context.Context, objectKey string) (storage.Object, error) {
	full, err := c.path(objectKey)
	if err != nil {
		return storage.Object{}, err
	}
	info, err := os.Stat(full)
	if os.IsNotExist(err) {
		return storage.Object{}, storage.ErrObjectNotFound
	}
	if err != nil {
		return storage.Object{}, fmt.Errorf("stat object: %w", err)
	}
	return storage.Object{
		ObjectKey: objectKey,
		Size:      info.Size(),
		Mime:      mimeOf(objectKey),
	}, nil
}

func (c *Client) PresignDownload(ctx context.Context, objectKey string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = c.cfg.DownloadTTL
	}
	expires := time.Now().Add(ttl)
	return c.signedURL("GET", objectKey, expires), expires, nil
}

func (c *Client) Remove(ctx context.Context, objectKey string) error {
	full, err := c.path(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove object: %w", err)
	}
	return nil
}

// --- what the gateway's routes call ---

// Verify checks a signed URL's query before the route acts on it. Exported because the route
// that serves the PUT and the GET lives in the gateway, and the signature is this package's
// business, not the transport's.
func (c *Client) Verify(method, objectKey, expires, signature string) error {
	at, err := strconv.ParseInt(expires, 10, 64)
	if err != nil {
		return storage.ErrObjectNotFound
	}
	if time.Now().After(time.Unix(at, 0)) {
		return storage.ErrObjectNotFound
	}
	want := c.sign(method, objectKey, at)
	// Constant time: a signature check that leaks its prefix is a signature check that can be
	// guessed one byte at a time.
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return storage.ErrObjectNotFound
	}
	return nil
}

// Write stores the bytes of a slot, refusing anything over the limit rather than filling the
// disk with an upload nobody agreed to.
func (c *Client) Write(objectKey string, src io.Reader) (int64, error) {
	full, err := c.path(objectKey)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return 0, fmt.Errorf("create object directory: %w", err)
	}
	dst, err := os.CreateTemp(filepath.Dir(full), ".partial-*")
	if err != nil {
		return 0, fmt.Errorf("open object for write: %w", err)
	}
	defer func() { _ = os.Remove(dst.Name()) }()
	// One byte more than the limit is how "exactly at the limit" is told from "over it".
	written, err := io.Copy(dst, io.LimitReader(src, c.cfg.MaxSize+1))
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("write object: %w", err)
	}
	if written > c.cfg.MaxSize {
		return 0, storage.ErrTooLarge
	}
	if err := os.Rename(dst.Name(), full); err != nil {
		return 0, fmt.Errorf("commit object: %w", err)
	}
	return written, nil
}

// Open reads an object back for the download route.
func (c *Client) Open(objectKey string) (io.ReadSeekCloser, string, error) {
	full, err := c.path(objectKey)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(full)
	if os.IsNotExist(err) {
		return nil, "", storage.ErrObjectNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("open object: %w", err)
	}
	return f, mimeOf(objectKey), nil
}

// --- internals ---

func (c *Client) allowed(mime string) bool {
	return slices.Contains(c.cfg.AllowedMimes, mime)
}

func (c *Client) signedURL(method, objectKey string, expires time.Time) string {
	at := expires.Unix()
	q := url.Values{
		ParamKey:       {objectKey},
		ParamExpires:   {strconv.FormatInt(at, 10)},
		ParamSignature: {c.sign(method, objectKey, at)},
	}
	return strings.TrimSuffix(c.cfg.BaseURL, "/") + ObjectPath + "?" + q.Encode()
}

// sign covers the method as well as the key: a slot signed for a write must not also be a
// readable link, and one signed for a read must not let anybody overwrite the object.
func (c *Client) sign(method, objectKey string, expires int64) string {
	mac := hmac.New(sha256.New, []byte(c.cfg.Secret))
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%d", method, objectKey, expires)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// path resolves a key under the root, refusing anything that is not a plain relative key.
// Refusing rather than clamping: `path.Clean` would turn "../escaped" into "escaped" and write
// somewhere that is safe but is not the key the caller named, and a stored object whose key
// differs from the `resource` row pointing at it is a picture nobody can find again.
func (c *Client) path(objectKey string) (string, error) {
	if !validKey(objectKey) {
		return "", storage.ErrObjectNotFound
	}
	root, err := filepath.Abs(c.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("resolve storage root: %w", err)
	}
	full := filepath.Join(root, filepath.FromSlash(objectKey))
	// Belt and braces: the shape check above already excludes an escape, and this catches a
	// platform where Join or FromSlash disagrees with that reading.
	if !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", storage.ErrObjectNotFound
	}
	return full, nil
}

// validKey is the shape a key may have: non-empty, relative, and every segment an ordinary
// name. No "..", no empty segment, no leading slash, no NUL.
func validKey(objectKey string) bool {
	if objectKey == "" || strings.HasPrefix(objectKey, "/") || strings.ContainsRune(objectKey, 0) {
		return false
	}
	for segment := range strings.SplitSeq(objectKey, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// objectKey is `<prefix>/<random>.<ext>`. The prefix groups a module's objects so an operator
// holding only a key can tell what it belongs to; the random half is why two uploads of the
// same filename do not collide.
func objectKey(prefix, filename string) string {
	name := randomName()
	if ext := strings.ToLower(path.Ext(filename)); ext != "" && len(ext) <= 8 && safeExt(ext) {
		name += ext
	}
	return path.Join(path.Clean("/"+strings.Trim(prefix, "/")), name)[1:]
}

func safeExt(ext string) bool {
	for _, r := range ext[1:] {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// mimeOf is the stored type, read back from the extension the key kept. Not authoritative —
// the `resource` row records what was declared — but it is what a download response needs.
func mimeOf(objectKey string) string {
	switch strings.ToLower(path.Ext(objectKey)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".pdf":
		return "application/pdf"
	}
	return "application/octet-stream"
}

// randomName is the stored half of a key. Random rather than derived from anything the caller
// sent: two uploads of "photo.jpg" must not be one object, and a key that can be guessed is a
// read of somebody else's upload once a link leaks.
func randomName() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail in practice, and a key with less entropy than asked for is
		// worse than no upload at all.
		panic("storage: read random: " + err.Error())
	}
	return hex.EncodeToString(raw[:])
}

package remote_test

import (
	"context"
	"errors"
	"testing"

	"shopnexus/internal/provider/storage"
	"shopnexus/internal/provider/storage/remote"
)

func TestPresignDownloadHandsBackTheKey(t *testing.T) {
	url := "https://cdn.example.com/file/abc123_tn"
	got, expires, err := remote.New().PresignDownload(context.Background(), url, 0)
	if err != nil {
		t.Fatalf("PresignDownload: %v", err)
	}
	if got != url {
		t.Errorf("url = %q, want the key verbatim", got)
	}
	// Zero, and deliberately: the link is the origin's, so this platform has no deadline to
	// claim for it. uploads.Resolve renders no url_expires_at when it sees this.
	if !expires.IsZero() {
		t.Errorf("expires = %v, want no expiry on a link this platform does not sign", expires)
	}
}

// The key goes into a page as a link, so anything a browser would execute has to be refused
// here rather than trusted to whatever renders it.
func TestPresignDownloadRejectsUnservableKeys(t *testing.T) {
	keys := []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"listing/2026/photo.jpg", // a relative key: that is another store's shape, not this one's
		"https://",               // no host
		"",
		"://nope",
	}
	for _, key := range keys {
		if _, _, err := remote.New().PresignDownload(context.Background(), key, 0); !errors.Is(err, storage.ErrObjectNotFound) {
			t.Errorf("PresignDownload(%q) err = %v, want ErrObjectNotFound", key, err)
		}
	}
}

func TestWritesAreRefused(t *testing.T) {
	c := remote.New()
	ctx := context.Background()
	if _, err := c.PresignUpload(ctx, storage.NewUpload{}); !errors.Is(err, storage.ErrProviderReadOnly) {
		t.Errorf("PresignUpload err = %v, want ErrProviderReadOnly", err)
	}
	if _, err := c.Stat(ctx, "https://cdn.example.com/x"); !errors.Is(err, storage.ErrProviderReadOnly) {
		t.Errorf("Stat err = %v, want ErrProviderReadOnly", err)
	}
	// Remove is the exception, and a success rather than a refusal: there is nothing of ours
	// to delete, and an error would put the reaper in a loop over a row it can never finish.
	if err := c.Remove(ctx, "https://cdn.example.com/x"); err != nil {
		t.Errorf("Remove err = %v, want a no-op success", err)
	}
}

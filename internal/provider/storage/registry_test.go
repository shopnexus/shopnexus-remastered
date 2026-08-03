package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"shopnexus/internal/provider/storage"
)

// fake is a store that only has to be tellable from another one by name.
type fake struct{ name string }

func (f fake) Name() string { return f.name }
func (f fake) PresignUpload(context.Context, storage.NewUpload) (storage.Upload, error) {
	return storage.Upload{}, nil
}
func (f fake) Stat(context.Context, string) (storage.Object, error) { return storage.Object{}, nil }
func (f fake) PresignDownload(context.Context, string, time.Duration) (string, time.Time, error) {
	return f.name + "-url", time.Time{}, nil
}
func (f fake) Remove(context.Context, string) error { return nil }

func TestRegistry(t *testing.T) {
	write, old := fake{"bucket"}, fake{"local"}
	reg, err := storage.NewRegistry(write, old)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if reg.Write().Name() != "bucket" {
		t.Errorf("Write() = %q, want the preferred store", reg.Write().Name())
	}
	// The point of the whole type: a row written before the switch still resolves against the
	// store that holds it, not against the one new uploads go to.
	for _, name := range []string{"bucket", "local"} {
		got, err := reg.For(name)
		if err != nil {
			t.Fatalf("For(%q): %v", name, err)
		}
		if got.Name() != name {
			t.Errorf("For(%q) returned %q", name, got.Name())
		}
	}
	if _, ok := reg.Lookup("local"); !ok {
		t.Error("Lookup(local) = false, want the write store's neighbour to be found")
	}
	if _, ok := reg.Lookup("s3"); ok {
		t.Error("Lookup(s3) = true, want a store nobody wired to be absent")
	}
}

// An unknown provider must not silently become the write store: that store would sign the key
// happily, and the caller would get a well-formed link to an object it has never held.
func TestRegistryForUnknownProviderFails(t *testing.T) {
	reg, err := storage.NewRegistry(fake{"local"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got, err := reg.For("s3")
	if !errors.Is(err, storage.ErrProviderUnknown) {
		t.Fatalf("For(s3) err = %v, want ErrProviderUnknown", err)
	}
	if got != nil {
		t.Errorf("For(s3) = %v, want no client", got)
	}
}

func TestNewRegistryRejectsBadWiring(t *testing.T) {
	if _, err := storage.NewRegistry(nil); err == nil {
		t.Error("NewRegistry(nil) succeeded, want a deployment with nowhere to write refused")
	}
	// Two stores under one name means a row cannot say which of them holds it.
	if _, err := storage.NewRegistry(fake{"local"}, fake{"local"}); err == nil {
		t.Error("duplicate provider accepted, want it refused at startup")
	}
}

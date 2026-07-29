package openapi_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"shopnexus/internal/shared/openapi"
)

// TestGeneratedSpecIsFresh fails if api/openapi.gen.yaml is out of date with the
// base + fragments — i.e. someone edited a fragment without running go generate.
func TestGeneratedSpecIsFresh(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := openapi.FindRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}

	want, err := openapi.Merge(root)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "api", "openapi.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated spec: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("api/openapi.gen.yaml is stale; run: go generate ./...")
	}
}

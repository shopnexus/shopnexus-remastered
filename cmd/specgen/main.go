// Command specgen merges the OpenAPI and AsyncAPI base documents and their
// per-module fragments into api/openapi.gen.yaml and api/asyncapi.gen.yaml (served,
// embedded, and published to docs). Run via `go generate ./...`.
package main

import (
	"log"
	"os"
	"path/filepath"

	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/openapi"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	root, err := openapi.FindRoot(cwd)
	if err != nil {
		log.Fatal(err)
	}

	// OpenAPI first: the AsyncAPI merge reads it for the schema closure, so a broken
	// REST fragment should fail here rather than as a confusing missing-schema error.
	write(filepath.Join(root, "api", "openapi.gen.yaml"), func() ([]byte, error) {
		return openapi.Merge(root)
	})
	write(filepath.Join(root, "api", "asyncapi.gen.yaml"), func() ([]byte, error) {
		return asyncapi.Merge(root)
	})
}

func write(out string, merge func() ([]byte, error)) {
	merged, err := merge()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, merged, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("specgen: wrote %s (%d bytes)", out, len(merged))
}

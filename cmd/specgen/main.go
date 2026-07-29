// Command specgen merges the OpenAPI base document and per-module fragments
// into api/openapi.gen.yaml (served, embedded, and published to docs).
// Run via `go generate ./...`.
package main

import (
	"log"
	"os"
	"path/filepath"

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
	merged, err := openapi.Merge(root)
	if err != nil {
		log.Fatal(err)
	}
	out := filepath.Join(root, "api", "openapi.gen.yaml")
	if err := os.WriteFile(out, merged, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("specgen: wrote %s (%d bytes)", out, len(merged))
}

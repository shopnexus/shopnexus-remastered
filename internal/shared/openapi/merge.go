// Package openapi merges the base document and per-module OpenAPI fragments
// (internal/<module>/api/openapi.yaml) into a single specification.
package openapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type doc = map[string]any

// FindRoot walks up from dir to the module root (the directory holding go.mod).
func FindRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("openapi: go.mod not found above " + dir)
		}
		dir = parent
	}
}

// MergeDoc reads api/openapi.base.yaml plus every internal/<module>/api/openapi.yaml
// under root and returns the merged specification as a document tree.
func MergeDoc(root string) (map[string]any, error) {
	base, err := readDoc(filepath.Join(root, "api", "openapi.base.yaml"))
	if err != nil {
		return nil, err
	}
	paths := child(base, "paths")
	schemas := child(child(base, "components"), "schemas")

	frags, err := filepath.Glob(filepath.Join(root, "internal", "module", "*", "api", "openapi.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob openapi fragments: %w", err)
	}
	sort.Strings(frags)
	for _, f := range frags {
		frag, err := readDoc(f)
		if err != nil {
			return nil, err
		}
		if err := mergeInto(paths, child(frag, "paths"), f, "path"); err != nil {
			return nil, err
		}
		fragSchemas := child(child(frag, "components"), "schemas")
		if err := mergeInto(schemas, fragSchemas, f, "schema"); err != nil {
			return nil, err
		}
	}
	return base, nil
}

// Merge returns the merged spec as deterministic YAML bytes (served/embedded
// and published to the docs site).
func Merge(root string) ([]byte, error) {
	d, err := MergeDoc(root)
	if err != nil {
		return nil, err
	}
	return RenderYAML(d)
}

// RenderYAML marshals a document to YAML.
func RenderYAML(d map[string]any) ([]byte, error) { return yaml.Marshal(d) }

func readDoc(path string) (doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read openapi doc %s: %w", path, err)
	}
	var d doc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("openapi: parse %s: %w", path, err)
	}
	if d == nil {
		d = doc{}
	}
	return d, nil
}

// child returns m[key] as a map, creating and storing it if absent.
func child(m doc, key string) doc {
	if v, ok := m[key].(doc); ok {
		return v
	}
	c := doc{}
	m[key] = c
	return c
}

// mergeInto copies every entry of src into dst, failing on a duplicate key so
// two modules can never silently claim the same path or schema name.
func mergeInto(dst, src doc, file, kind string) error {
	for k, v := range src {
		if _, exists := dst[k]; exists {
			return fmt.Errorf("openapi: duplicate %s %q (also defined in %s)", kind, k, file)
		}
		dst[k] = v
	}
	return nil
}

// Package specmerge holds the document-tree operations shared by the OpenAPI and
// AsyncAPI mergers.
//
// It is not a general YAML utility: MergeInto is where the duplicate-key invariant
// lives — two modules can never silently claim one name — and a second copy of that
// rule is a copy that can lose it while the first keeps it.
package specmerge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Doc is a parsed specification document.
type Doc = map[string]any

// FindRoot walks up from dir to the module root (the directory holding go.mod).
func FindRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("specmerge: go.mod not found above " + dir)
		}
		dir = parent
	}
}

// Read parses one YAML document, answering an empty Doc for an empty file.
func Read(path string) (Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec doc %s: %w", path, err)
	}
	var d Doc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("specmerge: parse %s: %w", path, err)
	}
	if d == nil {
		d = Doc{}
	}
	return d, nil
}

// Child returns m[key] as a Doc, creating and storing it if absent.
func Child(m Doc, key string) Doc {
	if v, ok := m[key].(Doc); ok {
		return v
	}
	c := Doc{}
	m[key] = c
	return c
}

// MergeInto copies every entry of src into dst, failing on a duplicate key so two
// modules can never silently claim the same name.
func MergeInto(dst, src Doc, file, kind string) error {
	for k, v := range src {
		if _, exists := dst[k]; exists {
			return fmt.Errorf("specmerge: duplicate %s %q (also defined in %s)", kind, k, file)
		}
		dst[k] = v
	}
	return nil
}

// RenderYAML marshals a document to YAML.
func RenderYAML(d Doc) ([]byte, error) { return yaml.Marshal(d) }

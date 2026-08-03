// Package openapi merges the base document and per-aggregate OpenAPI fragments
// (internal/module/<module>/api/openapi/<aggregate>.yaml) into a single
// specification.
package openapi

import (
	"fmt"
	"path/filepath"
	"sort"

	"shopnexus/internal/shared/specmerge"
)

// FindRoot walks up from dir to the module root. Re-exported so callers that only
// speak OpenAPI need one import.
func FindRoot(dir string) (string, error) { return specmerge.FindRoot(dir) }

// RenderYAML marshals a document to YAML.
func RenderYAML(d specmerge.Doc) ([]byte, error) { return specmerge.RenderYAML(d) }

// MergeDoc reads api/openapi.base.yaml plus every
// internal/module/<module>/api/openapi/*.yaml under root and returns the merged
// specification as a document tree.
//
// Only paths and components.schemas are merged: anything reusable across modules —
// parameters, responses, security schemes — belongs in the base document.
func MergeDoc(root string) (specmerge.Doc, error) {
	base, err := specmerge.Read(filepath.Join(root, "api", "openapi.base.yaml"))
	if err != nil {
		return nil, err
	}
	paths := specmerge.Child(base, "paths")
	schemas := specmerge.Child(specmerge.Child(base, "components"), "schemas")

	frags, err := filepath.Glob(filepath.Join(root, "internal", "module", "*", "api", "openapi", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob openapi fragments: %w", err)
	}
	sort.Strings(frags)
	for _, f := range frags {
		frag, err := specmerge.Read(f)
		if err != nil {
			return nil, err
		}
		if err := specmerge.MergeInto(paths, specmerge.Child(frag, "paths"), f, "path"); err != nil {
			return nil, err
		}
		fragSchemas := specmerge.Child(specmerge.Child(frag, "components"), "schemas")
		if err := specmerge.MergeInto(schemas, fragSchemas, f, "schema"); err != nil {
			return nil, err
		}
	}
	return base, nil
}

// Merge returns the merged spec as deterministic YAML bytes (served/embedded and
// published to the docs site).
func Merge(root string) ([]byte, error) {
	d, err := MergeDoc(root)
	if err != nil {
		return nil, err
	}
	return RenderYAML(d)
}

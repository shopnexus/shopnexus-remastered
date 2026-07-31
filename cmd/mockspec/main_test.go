package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"shopnexus/api"
)

func TestPrefixPathsMovesEveryPathAndKeepsTheRest(t *testing.T) {
	b, err := prefixPaths(api.SpecYAML, api.BasePath)
	if err != nil {
		t.Fatalf("prefixPaths: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	paths, ok := got["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("result has no paths")
	}
	for p := range paths {
		if !strings.HasPrefix(p, api.BasePath+"/") {
			t.Errorf("path %q is not under %s", p, api.BasePath)
		}
	}

	var orig map[string]any
	if err := yaml.Unmarshal(api.SpecYAML, &orig); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if len(paths) != len(orig["paths"].(map[string]any)) {
		t.Errorf("path count changed: %d, want %d", len(paths), len(orig["paths"].(map[string]any)))
	}
	if got["components"] == nil || got["info"] == nil {
		t.Error("components/info lost in the rewrite")
	}
}

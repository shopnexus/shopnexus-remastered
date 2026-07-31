// Command mockspec writes the served OpenAPI document with every path prefixed
// by api.BasePath, because Prism mounts `paths` as-is and ignores the relative
// `servers[0].url` — without the prefix a mock would answer /listings while the
// gateway answers /api/v1/listings, which is a difference a client discovers
// late. Reads the embedded spec, so it runs from the app image with no source
// tree: `mockspec /spec/openapi.mock.yaml`.
package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"

	"shopnexus/api"
	"shopnexus/internal/shared/openapi"
)

func main() {
	out := "openapi.mock.yaml"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	b, err := prefixPaths(api.SpecYAML, api.BasePath)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		log.Fatalf("write mock spec %s: %v", out, err)
	}
	log.Printf("mockspec: wrote %s (%d bytes), paths under %s", out, len(b), api.BasePath)
}

func prefixPaths(spec []byte, prefix string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi spec: %w", err)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapi spec has no paths object")
	}
	prefixed := make(map[string]any, len(paths))
	for p, v := range paths {
		prefixed[prefix+p] = v
	}
	doc["paths"] = prefixed
	b, err := openapi.RenderYAML(doc)
	if err != nil {
		return nil, fmt.Errorf("render mock spec: %w", err)
	}
	return b, nil
}

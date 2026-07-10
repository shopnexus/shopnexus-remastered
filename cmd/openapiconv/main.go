// Command openapiconv converts the swaggo-generated Swagger 2.0 spec
// (internal/openapi/swagger.json) into an OpenAPI 3.0 document
// (internal/openapi/openapi.v3.json). Mintlify only ingests OpenAPI 3.0/3.1,
// not Swagger 2.0, so this is the artifact the docs site consumes.
//
// Run via `make openapi` (it chains this after `swag init`). Do not hand-edit
// the output — it is regenerated from handler annotations.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
)

func main() {
	dir := "internal/openapi"
	in := filepath.Join(dir, "swagger.json")
	out := filepath.Join(dir, "openapi.v3.json")

	raw, err := os.ReadFile(in)
	if err != nil {
		log.Fatalf("read %s: %v (run `swag init` first)", in, err)
	}

	var doc2 openapi2.T
	if err := json.Unmarshal(raw, &doc2); err != nil {
		log.Fatalf("parse swagger 2.0: %v", err)
	}

	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		log.Fatalf("convert to openapi 3.0: %v", err)
	}
	if err := doc3.Validate(context.Background()); err != nil {
		log.Fatalf("converted spec failed validation: %v", err)
	}

	enc, err := json.MarshalIndent(doc3, "", "  ")
	if err != nil {
		log.Fatalf("marshal openapi 3.0: %v", err)
	}
	enc = append(enc, '\n')
	if err := os.WriteFile(out, enc, 0o644); err != nil {
		log.Fatalf("write %s: %v", out, err)
	}
	log.Printf("wrote %s (%d paths)", out, len(doc3.Paths.Map()))
}

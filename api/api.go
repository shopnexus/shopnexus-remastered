// Package api serves the OpenAPI spec and a Swagger UI page for the gateway's
// public REST surface. The served document is generated from a base file plus
// per-module fragments; edit those, not the generated file.
package api

import (
	_ "embed"
	"net/http"
)

//go:generate go run shopnexus/cmd/specgen

//go:embed openapi.gen.yaml
var specYAML []byte

// SpecYAML is the raw OpenAPI document, exposed for contract tests.
var SpecYAML = specYAML

// SpecHandler serves the raw OpenAPI document at /openapi.yaml.
func SpecHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(specYAML)
}

// DocsHandler serves a Swagger UI page (dev docs) that loads /openapi.yaml.
func DocsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>ShopNexus API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({ url: '/openapi.yaml', dom_id: '#swagger-ui' });
    };
  </script>
</body>
</html>
`

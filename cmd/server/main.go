package main

import (
	"flag"
	"os"
	"strings"

	"shopnexus-server/internal/app"

	// Blank import registers the generated OpenAPI spec (docs.SwaggerInfo) so the
	// in-app Swagger UI can serve it. Regenerate with `make openapi`.
	_ "shopnexus-server/internal/openapi"

	"go.uber.org/fx"
)

//	@title			ShopNexus API
//	@version		1.0
//	@description	Client-facing REST API for ShopNexus, a service-oriented e-commerce platform.
//	@description	Backend services coordinate internally over Restate; this spec covers only the HTTP gateway surface.

//	@BasePath	/api/v1

//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				JWT access token. Format: "Bearer &lt;token&gt;".

func main() {
	moduleFlag := flag.String("module", "", "comma-separated modules to run; empty = all (monolith)")
	flag.Parse()

	sel := *moduleFlag
	if sel == "" {
		sel = os.Getenv("APP_MODULE")
	}

	var opt fx.Option
	if sel == "" || sel == "all" {
		opt = app.Module
	} else {
		opt = app.Build(strings.Split(sel, ",")...)
	}
	fx.New(opt).Run()
}

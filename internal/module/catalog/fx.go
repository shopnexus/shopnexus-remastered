package catalog

import (
	catalogbiz "shopnexus-remastered/internal/module/catalog/biz"
	catalogecho "shopnexus-remastered/internal/module/catalog/transport/echo"

	"go.uber.org/fx"
)

// Module provides the catalog module dependencies
var Module = fx.Module("catalog",
	fx.Provide(
		catalogbiz.NewCatalogBiz,
		catalogecho.NewHandler,
	),
)

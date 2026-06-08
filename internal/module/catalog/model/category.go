package catalogmodel

import (
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
)

// Category is the domain-layer category: the DB row plus representative images
// (first resource of each popular product in the category).
type Category struct {
	catalogdb.CatalogCategory

	Resources []commonmodel.Resource `json:"resources"`
}

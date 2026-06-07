package catalogmodel

import (
	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
)

type Comment struct {
	catalogdb.CatalogComment

	Profile   accountmodel.Profile   `json:"profile"`
	Resources []commonmodel.Resource `json:"resources"`
}

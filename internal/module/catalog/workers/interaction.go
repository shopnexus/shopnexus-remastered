// Package catalogworkers subscribes catalog consumers to bus topics.
package catalogworkers

import (
	"context"

	"shopnexus-server/internal/infras/bus"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogconfig "shopnexus-server/internal/module/catalog/config"
)

func Register(cfg *catalogconfig.Config, bc bus.Client, catalog catalogbiz.CatalogBizClient) {
	bus.SubscribeBatch(bc, analyticmodel.TopicInteractionCreated, "catalog.search",
		func(ctx context.Context, events []analyticmodel.Interaction) error {
			return catalog.Guaranteed().Send().AddInteractions(ctx, events)
		},
		bus.WithBatchSize(cfg.Search.InteractionBatchSize),
		bus.WithLinger(cfg.Search.InteractionLinger),
	)
}

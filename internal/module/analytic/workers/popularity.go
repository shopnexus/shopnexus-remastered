// Package analyticworkers subscribes analytic consumers to bus topics.
package analyticworkers

import (
	"context"

	"shopnexus-server/internal/infras/bus"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
)

// Register wires analytic subscribers. Popularity scoring consumes recorded
// interactions; the call goes through the Restate client so processing is
// durable and retried.
func Register(bc bus.Client, analytic analyticbiz.AnalyticBizClient) {
	bus.Subscribe(bc, analyticmodel.TopicInteractionCreated, "analytic.popularity",
		func(ctx context.Context, event analyticmodel.Interaction) error {
			return analytic.Send().HandlePopularityEvent(ctx, event)
		})
}

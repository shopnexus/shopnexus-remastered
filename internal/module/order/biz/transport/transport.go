package transport

import (
	"context"

	accountbiz "shopnexus-server/internal/module/account/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/module/order/biz/base"
)

// TransportHandler implements TransportBiz over the shared core.
type TransportHandler struct {
	*base.Base

	account accountbiz.AccountBizClient
	catalog catalogbiz.CatalogBizClient
	common  commonbiz.CommonBizClient
}

// New builds the transport handler and registers its transport options in the
// central catalog.
func New(
	c *base.Base,
	account accountbiz.AccountBizClient,
	catalog catalogbiz.CatalogBizClient,
	common commonbiz.CommonBizClient,
) (*TransportHandler, error) {
	h := &TransportHandler{c, account, catalog, common}
	return h, h.SetupTransportMap()
}

// TransportBiz covers transport webhooks and shipping-cost quoting.
type TransportBiz interface {
	OnTransportResult(ctx context.Context, params OnTransportResultParams) error
	// QuoteTransport returns per-item shipping cost previews for the buyer's
	// checkout summary. Side-effect free — no inventory, no session.
	QuoteTransport(ctx context.Context, params QuoteTransportParams) (QuoteTransportResult, error)
}

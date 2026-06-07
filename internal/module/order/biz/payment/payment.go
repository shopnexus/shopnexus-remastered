package payment

import (
	"context"

	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	"shopnexus-server/internal/provider/payment"

	"github.com/google/uuid"
)

// PaymentHandler implements PaymentBiz over the shared core.
type PaymentHandler struct {
	*base.Base

	common      commonbiz.CommonBizClient
	checkout    checkout.CheckoutWfClient
	fulfillment fullfilment.FulfillmentWfClient
}

// New builds the payment handler and registers its payment options in the
// central catalog.
func New(
	c *base.Base,
	common commonbiz.CommonBizClient,
	checkout checkout.CheckoutWfClient,
	fulfillment fullfilment.FulfillmentWfClient,
) (*PaymentHandler, error) {
	h := &PaymentHandler{c, common, checkout, fulfillment}
	return h, h.SetupPaymentMap()
}

// PaymentBiz covers payment-result intake and gateway-URL reuse.
type PaymentBiz interface {
	OnPaymentResult(ctx context.Context, params payment.Notification) error
	GetReusableGatewayURL(ctx context.Context, sessionID uuid.UUID) (ReusableGatewayURLState, error)
}

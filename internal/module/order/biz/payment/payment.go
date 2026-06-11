package payment

import (
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	"shopnexus-server/internal/provider/payment"

	restate "github.com/restatedev/sdk-go"
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

// PaymentBiz covers payment-result intake.
type PaymentBiz interface {
	OnPaymentResult(ctx restate.Context, params payment.Notification) error
}

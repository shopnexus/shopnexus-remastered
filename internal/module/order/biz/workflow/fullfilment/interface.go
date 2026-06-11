package fullfilment

import (
	"context"

	"github.com/google/uuid"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

//go:generate go run shopnexus-server/cmd/genrestate -interface FulfillmentWf -service FulfillmentWorkflow -kind workflow

// FulfillmentWf is the typed client contract for FulfillmentWorkflow, keyed
// by order ID (= confirm session ID). Run awaits the whole workflow;
// submit-and-detach via Send().Run, then attach to WaitPaymentURL.
type FulfillmentWf interface {
	Run(ctx context.Context, orderID uuid.UUID, input FulfillmentInput) (FulfillmentOutput, error)
	WaitPaymentURL(ctx context.Context, orderID uuid.UUID) (string, error)
	RequestNewPaymentURL(ctx context.Context, orderID uuid.UUID) (string, error)
	PaymentNotification(ctx context.Context, orderID uuid.UUID, noti payment.Notification) error
	CancelConfirm(ctx context.Context, orderID uuid.UUID) error
	OnRefundChanged(ctx context.Context, orderID uuid.UUID) error
	OnBuyerWithdrew(ctx context.Context, orderID uuid.UUID, sig ordermodel.RefundSignal) error
	OnSellerDecision(ctx context.Context, orderID uuid.UUID, sig ordermodel.SellerDecisionSignal) error
	OnAdminDecision(ctx context.Context, orderID uuid.UUID, sig ordermodel.AdminDecisionSignal) error
	OnTransportDelivered(ctx context.Context, orderID uuid.UUID, sig ordermodel.TransportDeliveredSignal) error
}

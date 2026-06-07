package checkout

import (
	"context"

	"github.com/google/uuid"

	"shopnexus-server/internal/provider/payment"
)

//go:generate go run shopnexus-server/cmd/genrestate -interface CheckoutWf -service CheckoutWorkflow -kind workflow

// CheckoutWf is the typed client contract for CheckoutWorkflow, keyed by
// checkout session ID. Run awaits the whole workflow; submit-and-detach via
// Send().Run, then attach to WaitPaymentURL for the gateway redirect.
type CheckoutWf interface {
	Run(ctx context.Context, sessionID uuid.UUID, input CheckoutWorkflowInput) (CheckoutWorkflowOutput, error)
	WaitPaymentURL(ctx context.Context, sessionID uuid.UUID) (string, error)
	RequestNewPaymentURL(ctx context.Context, sessionID uuid.UUID) (string, error)
	PaymentNotification(ctx context.Context, sessionID uuid.UUID, noti payment.Notification) error
	CancelCheckout(ctx context.Context, sessionID uuid.UUID) error
}

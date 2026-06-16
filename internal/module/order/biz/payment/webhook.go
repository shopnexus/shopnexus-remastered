package payment

import (
	"fmt"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// OnPaymentResult is the unified entry point for gateway IPN webhooks.
func (b *PaymentHandler) OnPaymentResult(ctx restate.Context, params payment.Notification) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on payment result: %w", err)
	}

	txID, err := uuid.Parse(params.RefID)
	if err != nil {
		return fmt.Errorf("parse tx id: %w", err)
	}

	// decision: load the tx and its owning session to route the signal.
	session, err := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.PaymentSession, error) {
		tx, err := b.Storage.Querier().GetTransaction(rctx, txID)
		if err != nil {
			return ordermodel.PaymentSession{}, fmt.Errorf("get transaction: %w", err)
		}
		s, err := b.Storage.Querier().GetPaymentSession(rctx, tx.SessionID)
		if err != nil {
			return ordermodel.PaymentSession{}, fmt.Errorf("get session: %w", err)
		}
		return s, nil
	})
	if err != nil {
		return err
	}

	// tail: signal the owning workflow's payment_event promise. The cross-workflow
	// Send self-journals.
	switch session.Kind {
	case ordermodel.SessionKindBuyerCheckout:
		err = b.checkout.Send().PaymentNotification(ctx, session.ID, params)
	case ordermodel.SessionKindSellerConfirmationFee:
		err = b.fulfillment.Send().PaymentNotification(ctx, session.ID, params)
	}
	if err != nil {
		return fmt.Errorf("signal payment notification: %w", err)
	}
	return nil
}

// WorkflowForSession maps payment_session.kind to (workflowName, workflowID).
func WorkflowForSession(s ordermodel.PaymentSession) (workflowName, workflowID string) {
	switch s.Kind {
	case ordermodel.SessionKindBuyerCheckout:
		return "CheckoutWorkflow", s.ID.String()
	case ordermodel.SessionKindSellerConfirmationFee:
		return "FulfillmentWorkflow", s.ID.String()
	default:
		return "", ""
	}
}

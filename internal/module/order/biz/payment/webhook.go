package payment

import (
	"context"
	"fmt"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

// OnPaymentResult is the unified entry point for gateway IPN webhooks.
func (b *PaymentHandler) OnPaymentResult(ctx context.Context, params payment.Notification) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on payment result: %w", err)
	}

	txID, err := uuid.Parse(params.RefID)
	if err != nil {
		return fmt.Errorf("parse tx id: %w", err)
	}

	tx, err := b.Storage.Querier().GetTransaction(ctx, uuid.NullUUID{UUID: txID, Valid: true})
	if err != nil {
		return fmt.Errorf("get transaction: %w", err)
	}

	// load session + resolve TxID if the webhook didn't supply one.
	session, err := b.Storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: tx.SessionID, Valid: true})
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// signal owning workflow's payment_event promise.
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
func WorkflowForSession(s orderdb.OrderPaymentSession) (workflowName, workflowID string) {
	switch s.Kind {
	case ordermodel.SessionKindBuyerCheckout:
		return "CheckoutWorkflow", s.ID.String()
	case ordermodel.SessionKindSellerConfirmationFee:
		return "FulfillmentWorkflow", s.ID.String()
	default:
		return "", ""
	}
}

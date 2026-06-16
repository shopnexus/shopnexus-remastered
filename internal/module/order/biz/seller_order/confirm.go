package sellerorder

import (
	"context"
	"fmt"

	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

type ConfirmSellerPendingParams struct {
	Account       accountmodel.AuthenticatedAccount
	ItemIDs       []int64 `validate:"required,min=1,max=1000"`
	UseWallet     bool
	WalletID      uuid.NullUUID
	PaymentOption string `validate:"max=100"`
	Note          string `validate:"max=500"`
}

// ConfirmSellerPendingResult is the sync envelope bridging the async workflow
// submit into an HTTP response. ConfirmSessionID doubles as the workflow ID and
// the payment-gateway RefID. PaymentURL is empty for wallet-only confirms.
type ConfirmSellerPendingResult struct {
	ConfirmSessionID uuid.UUID
	PaymentURL       string
}

// ConfirmSellerPending submits a FulfillmentWorkflow and synchronously attaches
// to its shared GetPaymentURL handler. The workflow owns the saga lifecycle; we
// bridge the async submit into a sync result so the seller's UI can redirect to
// the gateway (or short-circuit for wallet-only confirms).
func (b *SellerHandler) ConfirmSellerPending(ctx context.Context, params ConfirmSellerPendingParams) (ConfirmSellerPendingResult, error) {
	if err := validator.Validate(params); err != nil {
		return ConfirmSellerPendingResult{}, fmt.Errorf("validate confirm items: %w", err)
	}
	// TODO: add lock to prevent concurrent confirms or reject on same items.

	workflowID := uuid.New()
	input := fullfilment.FulfillmentInput{
		Account:       params.Account,
		ItemIDs:       params.ItemIDs,
		UseWallet:     params.UseWallet,
		WalletID:      params.WalletID,
		PaymentOption: params.PaymentOption,
		Note:          params.Note,
	}

	// TODO: fix this: nếu user gửi 2 lần -> workflow submit 2 lần -> 2 workflow chạy song song -> 2 workflow
	if err := b.fulfillment.Send().Run(ctx, workflowID, input); err != nil {
		return ConfirmSellerPendingResult{}, fmt.Errorf("submit fulfillment workflow: %w", err)
	}

	url, err := b.fulfillment.GetPaymentURL(ctx, workflowID)
	if err != nil {
		return ConfirmSellerPendingResult{}, fmt.Errorf("get payment url: %w", err)
	}

	return ConfirmSellerPendingResult{
		ConfirmSessionID: workflowID,
		PaymentURL:       url,
	}, nil
}

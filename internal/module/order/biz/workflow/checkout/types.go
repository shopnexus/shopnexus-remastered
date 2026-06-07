package checkout

import (
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/module/order/biz/base"

	"github.com/google/uuid"
)

type CheckoutWorkflowInput struct {
	Account       accountmodel.AuthenticatedAccount `json:"account"`
	Items         []CheckoutItem                    `json:"items"               validate:"required,min=1,dive"`
	Address       string                            `json:"address"             validate:"required,min=1,max=500"`
	BuyNow        bool                              `json:"buy_now"`
	UseWallet     bool                              `json:"use_wallet"`
	WalletID      *uuid.UUID                        `json:"wallet_id,omitempty"`
	PaymentOption string                            `json:"payment_option"      validate:"max=100"`
}

type CheckoutWorkflowOutput struct {
	Status    string    `json:"status"`
	SessionID uuid.UUID `json:"session_id"`
}

// CheckoutItem is one line in a buyer checkout. Defined in base (the
// transport quote endpoint shares it); aliased here for the workflow input.
type CheckoutItem = base.CheckoutItem

package checkout

import (
	"encoding/json"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
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

// transportQuote is the resolved shipping option + cost for one cart line, in
// the seller's source currency.
type transportQuote struct {
	Option string `json:"option"`
	Cost   int64  `json:"cost"`
}

// itemAmounts holds the per-line amounts, already converted to buyer currency.
type itemAmounts struct {
	subtotalAmount int64
	totalAmount    int64
}

// pricing carries everything the later phases need from the price phase: the
// product lookup maps, the buyer's settlement currency, the FX snapshot to
// persist on the session, and the per-item amounts/quotes plus grand total.
type pricing struct {
	skuMap          map[uuid.UUID]catalogmodel.ProductSku
	spuMap          map[uuid.UUID]catalogmodel.ProductSpu
	buyerCurrency   string
	fxSnapshotJSON  json.RawMessage
	transportQuotes map[uuid.UUID]transportQuote
	itemAmounts     map[uuid.UUID]itemAmounts
	total           int64
}

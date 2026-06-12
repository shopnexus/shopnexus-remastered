package checkout

import (
	"encoding/json"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/module/order/biz/base"

	"github.com/google/uuid"
)

type CheckoutWorkflowInput struct {
	Account        accountmodel.AuthenticatedAccount `json:"account"`
	Items          []CheckoutItem                    `json:"items"               validate:"required,min=1,dive"`
	Address        string                            `json:"address"             validate:"required,min=1,max=500"`
	BuyNow         bool                              `json:"buy_now"`
	UseWallet      bool                              `json:"use_wallet"`
	WalletID       *uuid.UUID                        `json:"wallet_id,omitempty"`
	PaymentOption  string                            `json:"payment_option"      validate:"max=100"`
	PromotionCodes []string                          `json:"promotion_codes,omitempty" validate:"omitempty,dive,max=100"`
}

type CheckoutWorkflowOutput struct {
	Status    string    `json:"status"`
	SessionID uuid.UUID `json:"session_id"`
}

// CheckoutItem is aliased from base so the transport quote endpoint shares the same type.
type CheckoutItem = base.CheckoutItem

// transportQuote is the resolved shipping cost for one cart line, in the seller's source currency.
type transportQuote struct {
	Option string `json:"option"`
	Cost   int64  `json:"cost"`
}

// itemAmounts holds per-line amounts converted to buyer currency.
type itemAmounts struct {
	subtotalAmount int64
	totalAmount    int64
}

// pricing carries product maps, settlement currency, FX snapshot, and per-item amounts from the price phase.
type pricing struct {
	skuMap               map[uuid.UUID]catalogmodel.ProductSku
	spuMap               map[uuid.UUID]catalogmodel.ProductSpu
	buyerCurrency        string
	fxSnapshotJSON       json.RawMessage
	transportQuotes      map[uuid.UUID]transportQuote
	itemAmounts          map[uuid.UUID]itemAmounts
	total                int64
	appliedPromoCodes    []string // union of PromotionCodes applied across all items
	preDiscountTotal     int64    // total before promotions, for audit
}

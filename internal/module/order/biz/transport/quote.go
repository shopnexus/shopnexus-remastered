package transport

import (
	"fmt"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/module/order/biz/base"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// QuoteTransportParams asks the buyer-facing transport quote endpoint for a
// per-item shipping cost preview before the checkout workflow is submitted.
type QuoteTransportParams struct {
	Account accountmodel.AuthenticatedAccount `validate:"required"`
	Address string                            `validate:"required"`
	Items   []base.CheckoutItem               `validate:"required,min=1,dive"`
}

// QuoteTransportItemResult is the per-SKU quote: the cost is in the seller
// SPU's source currency (same convention as workflow_checkout step 5), so the
// FE must convert via the exchange-rate snapshot to the buyer's currency.
type QuoteTransportItemResult struct {
	SkuID    uuid.UUID `json:"sku_id"`
	Option   string    `json:"transport_option"`
	Cost     int64     `json:"cost"`
	Currency string    `json:"currency"`
}

type QuoteTransportResult struct {
	Items []QuoteTransportItemResult `json:"items"`
}

// QuoteTransport returns per-item shipping cost quotes without reserving
// inventory or creating any session. Mirrors the quote loop in
// CheckoutWorkflow.Run step 5 — kept in lockstep so the preview matches what
// the workflow will actually charge.
func (b *TransportHandler) QuoteTransport(
	ctx restate.Context,
	params QuoteTransportParams,
) (QuoteTransportResult, error) {
	var zero QuoteTransportResult

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate quote transport: %w", err)
	}

	skuIDs := lo.Map(params.Items, func(i base.CheckoutItem, _ int) uuid.UUID { return i.SkuID })

	skus, err := b.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{
		ID: skuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("fetch product skus: %w", err)
	}
	if len(skus) != len(skuIDs) {
		return zero, ordermodel.ErrOrderItemNotFound
	}

	listSpu, err := b.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		Account: params.Account,
		ID:      lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }),
	})
	if err != nil {
		return zero, fmt.Errorf("fetch product spus: %w", err)
	}

	skuMap := lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })
	spuMap := lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	sellerIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID {
		return spuMap[s.SpuID].AccountID
	}))

	sellerContacts, err := b.account.GetDefaultContact(ctx, sellerIDs)
	if err != nil {
		return zero, fmt.Errorf("fetch seller contacts: %w", err)
	}

	results := make([]QuoteTransportItemResult, 0, len(params.Items))
	for _, item := range params.Items {
		sku, ok := skuMap[item.SkuID]
		if !ok {
			return zero, ordermodel.ErrOrderItemNotFound
		}
		spu, ok := spuMap[sku.SpuID]
		if !ok {
			return zero, ordermodel.ErrOrderItemNotFound
		}

		transportClient, tcErr := b.GetTransportClient(item.TransportOption)
		if tcErr != nil {
			return zero, fmt.Errorf("get transport client: %w", tcErr)
		}

		sellerContact, ok := sellerContacts[spu.AccountID]
		if !ok {
			return zero, fmt.Errorf("seller contact not found: %w", ordermodel.ErrOrderItemNotFound)
		}

		quote, qErr := transportClient.Quote(ctx, transport.QuoteParams{
			Items: []transport.ItemMetadata{{
				SkuID:    item.SkuID,
				Quantity: item.Quantity,
			}},
			FromAddress: sellerContact.Address,
			ToAddress:   params.Address,
		})
		if qErr != nil {
			return zero, fmt.Errorf("quote transport for sku %s: %w", item.SkuID, qErr)
		}

		results = append(results, QuoteTransportItemResult{
			SkuID:    item.SkuID,
			Option:   item.TransportOption,
			Cost:     quote.Cost,
			Currency: spu.Currency,
		})
	}

	return QuoteTransportResult{Items: results}, nil
}

// See: https://docs.giaohangtietkiem.vn/webhook

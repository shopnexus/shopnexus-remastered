package checkout

import (
	"encoding/json"
	"fmt"

	restate "github.com/restatedev/sdk-go"

	accountbiz "shopnexus-server/internal/module/account/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commonmodel "shopnexus-server/internal/module/common/model"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/transport"
	sharedcurrency "shopnexus-server/internal/shared/currency"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// price loads the buyer profile, resolves and matches the address country,
// fetches the SKUs/SPUs, snapshots FX rates (validating availability up front),
// quotes transport per item, and computes the per-item and grand totals — all
// settled into the buyer's currency.
func (h *CheckoutWorkflow) price(ctx restate.WorkflowContext, input CheckoutWorkflowInput) (pricing, error) {
	var p pricing

	buyerProfile, err := h.account.GetProfile(ctx, accountbiz.GetProfileParams{AccountID: input.Account.ID})
	if err != nil {
		return p, fmt.Errorf("load buyer profile: %w", err)
	}

	resolvedCountry, err := h.common.ResolveCountry(ctx, input.Address)
	if err != nil {
		return p, fmt.Errorf("resolve country: %w", err)
	}
	if resolvedCountry != buyerProfile.Country {
		return p, ordermodel.ErrCheckoutAddressCountryMismatch.Fmt(resolvedCountry, buyerProfile.Country)
	}

	skuIDs := lo.Map(input.Items, func(s CheckoutItem, _ int) uuid.UUID { return s.SkuID })

	skus, err := h.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{ID: skuIDs})
	if err != nil {
		return p, fmt.Errorf("fetch product skus: %w", err)
	}
	if len(skus) != len(skuIDs) {
		return p, ordermodel.ErrOrderItemNotFound
	}

	listSpu, err := h.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		Account: input.Account,
		ID:      lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }),
	})
	if err != nil {
		return p, fmt.Errorf("fetch product spus: %w", err)
	}
	if len(listSpu.Data) == 0 {
		return p, ordermodel.ErrOrderItemNotFound
	}

	p.skuMap = lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })
	p.spuMap = lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	// FX snapshot. Mixed-currency carts are supported — each item converts from
	// its spu's own currency into the buyer's currency.
	p.buyerCurrency, err = sharedcurrency.Infer(buyerProfile.Country)
	if err != nil {
		return p, fmt.Errorf("infer buyer currency: %w", err)
	}

	needFX := false
	for _, spu := range listSpu.Data {
		if spu.Currency != p.buyerCurrency {
			needFX = true
			break
		}
	}

	var fxSnapshot commonmodel.ExchangeRateSnapshot
	if needFX {
		fxSnapshot, err = h.common.GetExchangeRates(ctx, commonbiz.GetExchangeRatesParams{})
		if err != nil {
			return p, fmt.Errorf("fx rate lookup: %w", err)
		}
		// Validate rate availability up front for every currency we'll touch
		// (buyer + each unique non-buyer seller currency). Fails loud before
		// inventory reservation, since ConvertAmountPure is fail-open.
		needed := map[string]struct{}{p.buyerCurrency: {}}
		for _, spu := range listSpu.Data {
			needed[spu.Currency] = struct{}{}
		}
		for c := range needed {
			if c == fxSnapshot.Base {
				continue
			}
			if r, ok := fxSnapshot.Rates[c]; !ok || r.Sign() <= 0 {
				return p, ordermodel.ErrFXRateUnavailable.Fmt(c)
			}
		}

		// The snapshot lives on the session (one source of truth across all
		// child txs) so audit can replay any per-item conversion via
		// (item.source_currency, session.fx_snapshot).
		raw, mErr := json.Marshal(fxSnapshot)
		if mErr != nil {
			return p, fmt.Errorf("marshal fx snapshot: %w", mErr)
		}
		p.fxSnapshotJSON = raw
	}

	convertToBuyer := func(amount int64, fromCurrency string) int64 {
		if fromCurrency == p.buyerCurrency {
			return amount
		}
		return commonbiz.ConvertAmountPure(amount, fromCurrency, p.buyerCurrency, fxSnapshot.Rates)
	}

	// Transport quotes per item.
	sellerIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID {
		return p.spuMap[s.SpuID].AccountID
	}))
	sellerContacts, err := h.account.GetDefaultContact(ctx, sellerIDs)
	if err != nil {
		return p, fmt.Errorf("fetch seller contacts: %w", err)
	}

	p.transportQuotes = make(map[uuid.UUID]transportQuote, len(input.Items))
	for _, item := range input.Items {
		spu := p.spuMap[p.skuMap[item.SkuID].SpuID]

		transportClient, tcErr := h.GetTransportClient(item.TransportOption)
		if tcErr != nil {
			return p, fmt.Errorf("get transport client: %w", tcErr)
		}
		sellerContact, ok := sellerContacts[spu.AccountID]
		if !ok {
			return p, fmt.Errorf("seller contact not found: %w", ordermodel.ErrOrderItemNotFound)
		}

		quote, qErr := transportClient.Quote(ctx, transport.QuoteParams{
			Items:       []transport.ItemMetadata{{SkuID: item.SkuID, Quantity: item.Quantity}},
			FromAddress: sellerContact.Address,
			ToAddress:   input.Address,
		})
		if qErr != nil {
			return p, fmt.Errorf("quote transport for sku %s: %w", item.SkuID, qErr)
		}
		p.transportQuotes[item.SkuID] = transportQuote{Option: item.TransportOption, Cost: quote.Cost}
	}

	// Per-item subtotal/total (converted to buyer currency) and grand total.
	p.itemAmounts = make(map[uuid.UUID]itemAmounts, len(input.Items))
	for _, item := range input.Items {
		sku := p.skuMap[item.SkuID]
		spu := p.spuMap[sku.SpuID]
		tq := p.transportQuotes[item.SkuID]
		subtotal := convertToBuyer(sku.Price*item.Quantity, spu.Currency)
		paid := subtotal + convertToBuyer(tq.Cost, spu.Currency)
		p.itemAmounts[item.SkuID] = itemAmounts{subtotalAmount: subtotal, totalAmount: paid}
		p.total += paid
	}

	return p, nil
}

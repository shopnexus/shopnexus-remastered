package checkout

import (
	"encoding/json"
	"fmt"

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

func (r *checkoutRun) price() error {
	ctx, input := r.ctx, r.input

	// Step 1: load buyer profile + resolve/verify shipping country
	buyerProfile, err := r.account.GetProfile(ctx, accountbiz.GetProfileParams{AccountID: input.Account.ID})
	if err != nil {
		return fmt.Errorf("load buyer profile: %w", err)
	}

	resolvedCountry, err := r.common.ResolveCountry(ctx, input.Address)
	if err != nil {
		return fmt.Errorf("resolve country: %w", err)
	}
	if resolvedCountry != buyerProfile.Country {
		return ordermodel.ErrCheckoutAddressCountryMismatch.Fmt(resolvedCountry, buyerProfile.Country)
	}

	// Step 2: load products (skus + spus) for the cart
	skuIDs := lo.Map(input.Items, func(s CheckoutItem, _ int) uuid.UUID { return s.SkuID })

	skus, err := r.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{ID: skuIDs})
	if err != nil {
		return fmt.Errorf("fetch product skus: %w", err)
	}
	if len(skus) != len(skuIDs) {
		return ordermodel.ErrOrderItemNotFound
	}

	listSpu, err := r.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		Account: input.Account,
		ID:      lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }),
	})
	if err != nil {
		return fmt.Errorf("fetch product spus: %w", err)
	}
	if len(listSpu.Data) == 0 {
		return ordermodel.ErrOrderItemNotFound
	}

	r.skuMap = lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })
	r.spuMap = lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	// Step 3: resolve buyer currency + validate FX snapshot up front
	// Each item converts from its spu's currency into the buyer's currency.
	r.buyerCurrency, err = sharedcurrency.Infer(buyerProfile.Country)
	if err != nil {
		return fmt.Errorf("infer buyer currency: %w", err)
	}

	needFX := false
	for _, spu := range listSpu.Data {
		if spu.Currency != r.buyerCurrency {
			needFX = true
			break
		}
	}

	var fxSnapshot commonmodel.ExchangeRateSnapshot
	if needFX {
		fxSnapshot, err = r.common.GetExchangeRates(ctx, commonbiz.GetExchangeRatesParams{})
		if err != nil {
			return fmt.Errorf("fx rate lookup: %w", err)
		}
		// ConvertAmountPure is fail-open; validate all rates up front before reservation.
		needed := map[string]struct{}{r.buyerCurrency: {}}
		for _, spu := range listSpu.Data {
			needed[spu.Currency] = struct{}{}
		}
		for c := range needed {
			if c == fxSnapshot.Base {
				continue
			}
			if rate, ok := fxSnapshot.Rates[c]; !ok || rate.Sign() <= 0 {
				return ordermodel.ErrFXRateUnavailable.Fmt(c)
			}
		}
		// Persisted on the session so audit can replay per-item conversions via (source_currency, fx_snapshot).
		raw, mErr := json.Marshal(fxSnapshot)
		if mErr != nil {
			return fmt.Errorf("marshal fx snapshot: %w", mErr)
		}
		r.fxSnapshotJSON = raw
	}

	convertToBuyer := func(amount int64, fromCurrency string) int64 {
		if fromCurrency == r.buyerCurrency {
			return amount
		}
		return commonbiz.ConvertAmountPure(amount, fromCurrency, r.buyerCurrency, fxSnapshot.Rates)
	}

	// Step 4: quote transport per item from each seller's contact address
	sellerIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID {
		return r.spuMap[s.SpuID].AccountID
	}))
	sellerContacts, err := r.account.GetDefaultContact(ctx, sellerIDs)
	if err != nil {
		return fmt.Errorf("fetch seller contacts: %w", err)
	}

	r.transportQuotes = make(map[uuid.UUID]transportQuote, len(input.Items))
	for _, item := range input.Items {
		spu := r.spuMap[r.skuMap[item.SkuID].SpuID]

		transportClient, tcErr := r.GetTransportClient(item.TransportOption)
		if tcErr != nil {
			return fmt.Errorf("get transport client: %w", tcErr)
		}
		sellerContact, ok := sellerContacts[spu.AccountID]
		if !ok {
			return fmt.Errorf("seller contact not found: %w", ordermodel.ErrOrderItemNotFound)
		}

		quote, qErr := transportClient.Quote(ctx, transport.QuoteParams{
			Items:       []transport.ItemMetadata{{SkuID: item.SkuID, Quantity: item.Quantity}},
			FromAddress: sellerContact.Address,
			ToAddress:   input.Address,
		})
		if qErr != nil {
			return fmt.Errorf("quote transport for sku %s: %w", item.SkuID, qErr)
		}
		r.transportQuotes[item.SkuID] = transportQuote{Option: item.TransportOption, Cost: quote.Cost}
	}

	// Step 5: convert per-item subtotal + transport into buyer currency and sum the total
	r.itemAmounts = make(map[uuid.UUID]itemAmounts, len(input.Items))
	for _, item := range input.Items {
		sku := r.skuMap[item.SkuID]
		spu := r.spuMap[sku.SpuID]
		tq := r.transportQuotes[item.SkuID]
		subtotal := convertToBuyer(sku.Price*item.Quantity, spu.Currency)
		paid := subtotal + convertToBuyer(tq.Cost, spu.Currency)
		r.itemAmounts[item.SkuID] = itemAmounts{subtotalAmount: subtotal, totalAmount: paid}
		r.total += paid
	}

	return nil
}

package base

import (
	"context"
	"fmt"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// EnrichItems hydrates each item with its product slug + primary image so the
// FE can render cards without N hops.
func (b *Base) EnrichItems(ctx context.Context, items []ordermodel.OrderItem) ([]ordermodel.OrderItem, error) {
	if len(items) == 0 {
		return []ordermodel.OrderItem{}, nil
	}

	skuIDs := lo.Uniq(lo.Map(items, func(it ordermodel.OrderItem, _ int) uuid.UUID { return it.SkuID }))
	skus, err := b.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{ID: skuIDs})
	if err != nil {
		return nil, fmt.Errorf("enrich items: list skus: %w", err)
	}
	skuMap := lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })

	spuIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }))
	listSpu, err := b.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{ID: spuIDs})
	if err != nil {
		return nil, fmt.Errorf("enrich items: list spus: %w", err)
	}
	spuMap := lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	resources, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("enrich items: get resources: %w", err)
	}

	result := make([]ordermodel.OrderItem, 0, len(items))
	for _, it := range items {
		spuID := it.SpuID
		if sku, ok := skuMap[it.SkuID]; ok {
			spuID = sku.SpuID
		}
		if spu, ok := spuMap[spuID]; ok {
			it.Slug = spu.Slug
		}
		if res, ok := resources[spuID]; ok && len(res) > 0 {
			it.ImageURL = res[0].Url
		}
		result = append(result, it)
	}

	return result, nil
}

func (b *Base) HydrateOrders(ctx context.Context, orders []ordermodel.Order) ([]ordermodel.Order, error) {
	if len(orders) == 0 {
		return []ordermodel.Order{}, nil
	}

	orderIDs := lo.Map(orders, func(o ordermodel.Order, _ int) uuid.UUID { return o.ID })
	transportIDs := lo.Uniq(lo.Map(orders, func(o ordermodel.Order, _ int) int64 { return o.TransportID }))

	orderItemsRes, err := b.Storage.Querier().ListItem(ctx, orderrepo.ListItemParams{
		OrderId: orderIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db fetch order items: %w", err)
	}
	orderItems := orderItemsRes.Data
	transportsRes, err := b.Storage.Querier().ListTransport(ctx, orderrepo.ListTransportParams{Id: transportIDs})
	if err != nil {
		return nil, fmt.Errorf("db fetch transports: %w", err)
	}
	transports := transportsRes.Data

	allEnriched, err := b.EnrichItems(ctx, orderItems)
	if err != nil {
		return nil, fmt.Errorf("enrich order items: %w", err)
	}

	enrichedItemsMap := make(map[uuid.UUID][]ordermodel.OrderItem)
	for _, item := range allEnriched {
		if item.OrderID.Valid {
			enrichedItemsMap[item.OrderID.UUID] = append(enrichedItemsMap[item.OrderID.UUID], item)
		}
	}

	transportMap := lo.KeyBy(transports, func(t ordermodel.Transport) int64 { return t.ID })

	result := make([]ordermodel.Order, 0, len(orders))
	for _, o := range orders {
		if t, ok := transportMap[o.TransportID]; ok {
			t := t
			o.Transport = &t
		}
		o.Items = enrichedItemsMap[o.ID]

		confirmSession, err := b.Storage.Querier().GetPaymentSession(ctx, o.ConfirmSessionID)
		if err != nil {
			return nil, fmt.Errorf("get confirm session: %w", err)
		}
		total, err := b.Storage.Querier().SumTotalAmountByOrder(ctx, uuid.NullUUID{UUID: o.ID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("sum paid amount by order: %w", err)
		}

		o.TotalAmount = total
		o.ConfirmSession = &confirmSession
		if payoutSession, perr := b.Storage.Querier().GetPayoutSessionForOrder(ctx, o.ID); perr == nil {
			payoutSession := payoutSession
			o.PayoutSession = &payoutSession
		}

		result = append(result, o)
	}

	return result, nil
}

func (b *Base) HydrateRefunds(ctx context.Context, rows ...ordermodel.Refund) ([]ordermodel.Refund, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefund,
		RefIDs:  lo.Map(rows, func(r ordermodel.Refund, _ int) uuid.UUID { return r.ID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list refund resources: %w", err)
	}

	return lo.Map(rows, func(r ordermodel.Refund, _ int) ordermodel.Refund {
		r.Resources = resourcesMap[r.ID]
		return r
	}), nil
}

func (b *Base) HydrateRefundDisputes(ctx context.Context, rows ...ordermodel.RefundDispute) ([]ordermodel.RefundDispute, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefundDispute,
		RefIDs:  lo.Map(rows, func(r ordermodel.RefundDispute, _ int) uuid.UUID { return r.ID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list dispute resources: %w", err)
	}

	return lo.Map(rows, func(r ordermodel.RefundDispute, _ int) ordermodel.RefundDispute {
		r.Resources = resourcesMap[r.ID]
		return r
	}), nil
}

package base

import (
	"context"
	"fmt"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/repolist"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// EnrichItems converts DB items to model items and hydrates each with its
// product slug + primary image so the FE can render cards without N hops.
func (b *Base) EnrichItems(ctx context.Context, dbItems []orderdb.OrderItem) ([]ordermodel.OrderItem, error) {
	if len(dbItems) == 0 {
		return []ordermodel.OrderItem{}, nil
	}

	skuIDs := lo.Uniq(lo.Map(dbItems, func(it orderdb.OrderItem, _ int) uuid.UUID { return it.SkuID }))
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

	result := make([]ordermodel.OrderItem, 0, len(dbItems))
	for _, it := range dbItems {
		richit := ordermodel.OrderItem{
			OrderItem: it,
		}
		spuID := it.SpuID
		if sku, ok := skuMap[it.SkuID]; ok {
			spuID = sku.SpuID
		}
		if spu, ok := spuMap[spuID]; ok {
			richit.Slug = spu.Slug
		}
		if res, ok := resources[spuID]; ok && len(res) > 0 {
			richit.ImageURL = res[0].Url
		}
		result = append(result, richit)
	}

	return result, nil
}

func (b *Base) HydrateOrders(ctx context.Context, orders []orderdb.OrderOrder) ([]ordermodel.Order, error) {
	if len(orders) == 0 {
		return []ordermodel.Order{}, nil
	}

	orderIDs := lo.Map(orders, func(o orderdb.OrderOrder, _ int) uuid.UUID { return o.ID })
	transportIDs := lo.Uniq(lo.Map(orders, func(o orderdb.OrderOrder, _ int) int64 { return o.TransportID }))

	orderItemsRes, err := b.Storage.Querier().ListItem(ctx, repolist.Request{}, orderdb.ListItemFilter{
		OrderId: orderIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db fetch order items: %w", err)
	}
	orderItems := orderItemsRes.Data
	transportsRes, err := b.Storage.Querier().ListTransport(ctx, repolist.Request{}, orderdb.ListTransportFilter{Id: transportIDs})
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

	transportMap := lo.KeyBy(transports, func(t orderdb.OrderTransport) int64 { return t.ID })

	result := make([]ordermodel.Order, 0, len(orders))
	for _, o := range orders {
		richOrder := ordermodel.Order{OrderOrder: o}
		if t, ok := transportMap[o.TransportID]; ok {
			richOrder.Transport = &t
		}
		richOrder.Items = enrichedItemsMap[o.ID]

		confirmSession, err := b.Storage.Querier().
			GetPaymentSession(ctx, uuid.NullUUID{UUID: o.ConfirmSessionID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("get confirm session: %w", err)
		}
		total, err := b.Storage.Querier().SumTotalAmountByOrder(ctx, uuid.NullUUID{UUID: o.ID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("sum paid amount by order: %w", err)
		}

		richOrder.TotalAmount = total
		richOrder.ConfirmSession = &confirmSession
		if payoutSession, perr := b.Storage.Querier().GetPayoutSessionForOrder(ctx, o.ID); perr == nil {
			richOrder.PayoutSession = &payoutSession
		}

		result = append(result, richOrder)
	}

	return result, nil
}

func (b *Base) HydrateRefunds(ctx context.Context, rows ...orderdb.OrderRefund) ([]ordermodel.Refund, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	// Map resources to refunds
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefund,
		RefIDs:  lo.Map(rows, func(r orderdb.OrderRefund, _ int) uuid.UUID { return r.ID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list refund resources: %w", err)
	}

	return lo.Map(rows, func(r orderdb.OrderRefund, _ int) ordermodel.Refund {
		return ordermodel.Refund{
			OrderRefund: r,
			Resources:   resourcesMap[r.ID],
		}
	}), nil
}

func (b *Base) HydrateRefundDisputes(ctx context.Context, rows ...orderdb.OrderRefundDispute) ([]ordermodel.RefundDispute, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	// Map resources to disputes
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefundDispute,
		RefIDs:  lo.Map(rows, func(r orderdb.OrderRefundDispute, _ int) uuid.UUID { return r.ID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list dispute resources: %w", err)
	}

	return lo.Map(rows, func(r orderdb.OrderRefundDispute, _ int) ordermodel.RefundDispute {
		return ordermodel.RefundDispute{
			OrderRefundDispute: r,
			Resources:          resourcesMap[r.ID],
		}
	}), nil
}

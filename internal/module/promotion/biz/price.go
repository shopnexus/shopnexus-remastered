package promotionbiz

import (
	"fmt"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
	promotionmodel "shopnexus-server/internal/module/promotion/model"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

const PromotionGroupExclusive = "exclusive"

// DiscountData represents the JSONB data for Discount and ShipDiscount promotion types.
type DiscountData struct {
	MinSpend        int64    `json:"min_spend"`
	MaxDiscount     int64    `json:"max_discount"`
	DiscountPercent *float64 `json:"discount_percent,omitempty"` // 0.0 – 1.0
	DiscountPrice   *int64   `json:"discount_price,omitempty"`   // fixed amount off
}

// parsedPromotion holds a promotion with its pre-parsed discount data.
type parsedPromotion struct {
	promotionmodel.Promotion

	discount *DiscountData // nil if data is empty or unparseable
}

// groupWinner is the best promotion selected within a single stacking group.
type groupWinner struct {
	promo       parsedPromotion
	productCost int64
	shipCost    int64
}

type CalculatePromotedPricesParams struct {
	Prices []catalogmodel.RequestOrderPrice
	SpuMap map[uuid.UUID]promotionmodel.PromoSpu
}

// CalculatePromotedPrices calculates promoted prices for the given SKUs.
// Group-based stacking: different groups stack, same group picks the best.
func (s *PromotionHandler) CalculatePromotedPrices(
	ctx restate.Context,
	params CalculatePromotedPricesParams,
) (map[uuid.UUID]*catalogmodel.OrderPrice, error) {
	prices := params.Prices
	spuMap := params.SpuMap
	// Collect all manually-entered promotion codes
	var codes []string
	for _, p := range prices {
		codes = append(codes, p.PromotionCodes...)
	}

	// Initialize output map
	priceMap := make(map[uuid.UUID]*catalogmodel.OrderPrice, len(prices))
	for _, p := range prices {
		priceMap[p.SkuID] = &catalogmodel.OrderPrice{Request: p}
	}

	// Fetch active promotions (auto-apply + code-matched)
	dbPromos, err := s.storage.Querier().ListActivePromotion(ctx, promotiondb.ListActivePromotionParams{
		AutoApply: null.BoolFrom(true),
		Code:      codes,
	})
	if err != nil {
		return nil, fmt.Errorf("db list active promotions: %w", err)
	}
	if len(dbPromos) == 0 {
		// No active promotions — set original prices and return early
		for _, p := range prices {
			op := priceMap[p.SkuID]
			op.ProductCost = p.UnitPrice * p.Quantity
			op.ShipCost = p.ShipCost
		}
		return priceMap, nil
	}

	// Batch-load refs for all promotions
	promoIDs := lo.Map(dbPromos, func(p promotiondb.PromotionPromotion, _ int) uuid.UUID { return p.ID })
	refsRes, err := s.storage.Querier().ListRef(ctx, promotiondb.ListRefParams{PromotionId: promoIDs})
	if err != nil {
		return nil, fmt.Errorf("db list promotion refs: %w", err)
	}
	refsMap := lo.GroupBy(refsRes.Data, func(r promotiondb.PromotionRef) uuid.UUID { return r.PromotionID })

	// Build parsed promotions: unmarshal JSON once per promotion (not per SKU)
	promos := make([]parsedPromotion, 0, len(dbPromos))
	for _, p := range dbPromos {
		promos = append(promos, parsedPromotion{
			Promotion: mapPromotion(p, refsMap[p.ID]),
			discount:  s.parseDiscountData(p.ID, p.Data),
		})
	}

	// Reusable buffer for applicable promotions per SKU
	applicable := make([]parsedPromotion, 0, len(promos))

	for _, price := range prices {
		op := priceMap[price.SkuID]
		spu := spuMap[price.SpuID]
		originalProduct := price.UnitPrice * price.Quantity
		originalShip := price.ShipCost

		// Filter applicable promotions (reuse slice)
		applicable = applicable[:0]
		for _, p := range promos {
			if isApplicable(p.Promotion, spu, price.SkuID) {
				applicable = append(applicable, p)
			}
		}

		if len(applicable) == 0 {
			op.ProductCost = originalProduct
			op.ShipCost = originalShip
			continue
		}

		// Group by stacking group → pick best winner per group
		grouped := lo.GroupBy(applicable, func(p parsedPromotion) string { return p.Group })

		var winners []groupWinner
		for _, group := range grouped {
			best := pickBestInGroup(group, originalProduct, originalShip)
			if best != nil {
				winners = append(winners, *best)
			}
		}

		// Apply winners
		applyWinners(op, winners, originalProduct, originalShip)
	}

	return priceMap, nil
}

// pickBestInGroup selects the promotion that gives the biggest total savings.
// On equal savings, higher priority wins.
func pickBestInGroup(group []parsedPromotion, originalProduct, originalShip int64) *groupWinner {
	var best *groupWinner

	for _, promo := range group {
		productCost := originalProduct
		shipCost := originalShip

		if promo.discount != nil {
			switch promo.Type {
			case promotionmodel.TypeDiscount:
				productCost = applyDiscount(originalProduct, promo.discount)
			case promotionmodel.TypeShipDiscount:
				shipCost = applyDiscount(originalShip, promo.discount)
			}
		}

		totalCost := productCost + shipCost
		if best == nil {
			best = &groupWinner{promo: promo, productCost: productCost, shipCost: shipCost}
			continue
		}

		bestTotal := best.productCost + best.shipCost
		// Prefer bigger savings; on tie, prefer higher priority
		if totalCost < bestTotal || (totalCost == bestTotal && promo.Priority > best.promo.Priority) {
			best = &groupWinner{promo: promo, productCost: productCost, shipCost: shipCost}
		}
	}

	return best
}

// applyWinners sets the final price on an OrderPrice from the group winners.
func applyWinners(
	op *catalogmodel.OrderPrice,
	winners []groupWinner,
	originalProduct, originalShip int64,
) {
	// Check for exclusive group — only that winner applies
	for _, w := range winners {
		if w.promo.Group == PromotionGroupExclusive {
			op.ProductCost = w.productCost
			op.ShipCost = w.shipCost
			op.PromotionCodes = []string{w.promo.Code}
			return
		}
	}

	// Stack all group winners
	op.ProductCost = originalProduct
	op.ShipCost = originalShip

	for _, w := range winners {
		op.PromotionCodes = append(op.PromotionCodes, w.promo.Code)

		switch w.promo.Type {
		case promotionmodel.TypeDiscount:
			op.ProductCost = w.productCost
		case promotionmodel.TypeShipDiscount:
			op.ShipCost = w.shipCost
		}
	}
}

// isApplicable checks if a promotion applies to a given SKU via ref matching.
// A promotion with no refs applies to everything.
func isApplicable(promo promotionmodel.Promotion, spu promotionmodel.PromoSpu, skuID uuid.UUID) bool {
	if len(promo.Refs) == 0 {
		return true
	}
	for _, ref := range promo.Refs {
		switch ref.RefType {
		case promotionmodel.RefTypeCategory:
			if spu.CategoryID == ref.RefID {
				return true
			}
		case promotionmodel.RefTypeProductSpu:
			if spu.ID == ref.RefID {
				return true
			}
		case promotionmodel.RefTypeProductSku:
			if skuID == ref.RefID {
				return true
			}
		}
	}
	return false
}

// applyDiscount calculates the price after applying a discount.
func applyDiscount(originalPrice int64, d *DiscountData) int64 {
	if d == nil {
		return originalPrice
	}

	minSpend := d.MinSpend
	if originalPrice < minSpend {
		return originalPrice
	}

	maxDiscount := d.MaxDiscount
	var discount int64

	if d.DiscountPercent != nil {
		discount = min(int64(float64(originalPrice)*(*d.DiscountPercent)), maxDiscount)
	} else if d.DiscountPrice != nil {
		discount = min(*d.DiscountPrice, maxDiscount)
	}

	return max(originalPrice-discount, 0)
}

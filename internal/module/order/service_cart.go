package order

import (
	"context"
	"fmt"

	catalogapi "shopnexus/internal/module/catalog/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/id"
)

// ListCartItems answers the caller's saved intentions, newest first.
func (s *Service) ListCartItems(ctx context.Context, req orderapi.ListCartRequest) ([]orderapi.CartItem, error) {
	rows, err := s.repo.ListCartItems(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	out := make([]orderapi.CartItem, 0, len(rows))
	for _, c := range rows {
		out = append(out, toAPICartItem(c))
	}
	return out, nil
}

// AddCartItem saves a variant, or tops up the row that is already there — the cart is keyed
// by (account, variant), so adding twice means wanting more rather than having two.
//
// The listing is resolved here rather than trusted: a cart row has to name its listing to
// be renderable at all, and only catalog knows which listing a variant belongs to.
func (s *Service) AddCartItem(ctx context.Context, req orderapi.AddCartItemRequest) (orderapi.CartItem, error) {
	listing, variant, err := s.variantOf(ctx, req.ActorID, req.VariantID)
	if err != nil {
		return orderapi.CartItem{}, err
	}
	_ = variant
	c, err := domain.NewCartItem(req.ActorID.Int64(), listing.ID.Int64(), req.VariantID.Int64(), req.Quantity)
	if err != nil {
		return orderapi.CartItem{}, err
	}
	if err := s.repo.UpsertCartItem(ctx, &c); err != nil {
		return orderapi.CartItem{}, fmt.Errorf("upsert cart item: %w", err)
	}
	return toAPICartItem(c), nil
}

// UpdateCartItem sets the quantity outright. Zero is not "remove": that is a DELETE, so
// there is one way to spell each intention.
func (s *Service) UpdateCartItem(ctx context.Context, req orderapi.UpdateCartItemRequest) (orderapi.CartItem, error) {
	c, err := s.repo.FindCartItem(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return orderapi.CartItem{}, fmt.Errorf("find cart item: %w", err)
	}
	if err := c.SetQuantity(req.Quantity); err != nil {
		return orderapi.CartItem{}, err
	}
	if err := s.repo.SaveCartItem(ctx, c); err != nil {
		return orderapi.CartItem{}, fmt.Errorf("save cart item: %w", err)
	}
	return toAPICartItem(c), nil
}

func (s *Service) DeleteCartItem(ctx context.Context, req orderapi.CartItemRequest) error {
	if err := s.repo.DeleteCartItem(ctx, req.ID.Int64(), req.ActorID.Int64()); err != nil {
		return fmt.Errorf("delete cart item: %w", err)
	}
	return nil
}

// variantOf resolves a variant to its listing through catalog, which is the only place that
// knows the mapping — a variant is not addressable on its own in that contract, so the
// listing is read and the variant found inside it.
func (s *Service) variantOf(ctx context.Context, viewerID id.ID[id.Account], variantID id.ID[id.Variant]) (catalogapi.ListingDetail, catalogapi.Variant, error) {
	page, err := s.catalog.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: viewerID, Variants: []id.ID[id.Variant]{variantID}, Page: 1, Limit: 1,
	})
	if err != nil {
		return catalogapi.ListingDetail{}, catalogapi.Variant{}, fmt.Errorf("resolve variant: %w", err)
	}
	if len(page.Data) == 0 {
		return catalogapi.ListingDetail{}, catalogapi.Variant{}, domain.ErrVariantNotInDraft
	}
	listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
		ID: page.Data[0].ID, ViewerID: viewerID,
	})
	if err != nil {
		return catalogapi.ListingDetail{}, catalogapi.Variant{}, fmt.Errorf("get listing: %w", err)
	}
	for _, v := range listing.Variants {
		if v.ID == variantID {
			return listing, v, nil
		}
	}
	return catalogapi.ListingDetail{}, catalogapi.Variant{}, domain.ErrVariantNotInDraft
}

func toAPICartItem(c domain.CartItem) orderapi.CartItem {
	return orderapi.CartItem{
		ID:        id.Of[id.CartItem](c.ID),
		ListingID: id.Of[id.Listing](c.ListingID),
		VariantID: id.Of[id.Variant](c.VariantID),
		Quantity:  c.Quantity,
		CreatedAt: c.CreatedAt,
	}
}

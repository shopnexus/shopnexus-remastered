package catalog

import (
	"context"
	"fmt"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
)

// CreateVariant adds a variant to a listing the caller owns. Not moderated: a seller
// restocking a size or adding a colour is not a claim about what the listing is.
func (s *Service) CreateVariant(ctx context.Context, req catalogapi.CreateVariantRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingForSeller(ctx, req.ListingID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	v, err := domain.NewVariant(domain.NewVariantInput{
		Price:          req.Price,
		Attributes:     req.Attributes,
		PackageDetails: req.PackageDetails,
		Attachments:    resourceKeys(req.Attachments),
		Quantity:       req.Quantity,
	})
	if err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := l.AddVariant(v); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.requireResources(ctx, l); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

// UpdateVariant patches one variant. Absent leaves a field alone; there is no clear flag
// because every field here is NOT NULL, and quantity is refused below what is committed.
func (s *Service) UpdateVariant(ctx context.Context, req catalogapi.UpdateVariantRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingByVariant(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing by variant: %w", err)
	}
	v, err := l.Variant(req.ID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, err
	}
	// What the patch touches is also what the trail records: a price change is the edit a
	// seller makes most, and nothing else on this path would name it.
	var edited []string
	if req.Price != nil {
		v.Price = *req.Price
		edited = append(edited, "price")
	}
	if req.Attributes != nil {
		v.Attributes = req.Attributes
		edited = append(edited, "attributes")
	}
	if req.PackageDetails != nil {
		v.PackageDetails = req.PackageDetails
		edited = append(edited, "package_details")
	}
	if req.Attachments != nil {
		v.Attachments = resourceKeys(req.Attachments)
		edited = append(edited, "attachments")
	}
	if req.Quantity != nil {
		if err := v.Stock.SetQuantity(*req.Quantity); err != nil {
			return catalogapi.ListingDetail{}, err
		}
		edited = append(edited, "quantity")
	}
	// Featuring is a rule about the set, so it goes through the root rather than the field.
	if req.IsFeatured != nil && *req.IsFeatured {
		if err := l.SetFeatured(v.ID); err != nil {
			return catalogapi.ListingDetail{}, err
		}
		edited = append(edited, "is_featured")
	}
	l.NoteVariantEdited(v.ID, edited)
	if err := s.requireResources(ctx, l); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

func (s *Service) DeleteVariant(ctx context.Context, req catalogapi.DeleteVariantRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingByVariant(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing by variant: %w", err)
	}
	if err := l.RemoveVariant(req.ID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

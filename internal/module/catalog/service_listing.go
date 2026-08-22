package catalog

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// CreateListing writes the listing and its variants in one transaction. The request carries
// at least one variant, so a listing with nothing to sell is not a state a client can reach.
func (s *Service) CreateListing(ctx context.Context, req catalogapi.CreateListingRequest) (catalogapi.ListingDetail, error) {
	// Selling requires a verified identity: payouts are gated on the same flag, and finding
	// that out after the first sale is worse than finding it out now.
	if err := s.requireSeller(ctx, req.ActorID); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	variants := make([]domain.NewVariantInput, 0, len(req.Variants))
	for _, in := range req.Variants {
		variants = append(variants, domain.NewVariantInput{
			Price:          in.Price,
			Attributes:     in.Attributes,
			PackageDetails: in.PackageDetails,
			Attachments:    resourceKeys(in.Attachments),
			Quantity:       in.Quantity,
		})
	}
	l, err := domain.NewListing(req.ActorID.Int64(), req.CategoryID.Int64(), domain.NewListingInput{
		Name:           req.Name,
		Description:    req.Description,
		Condition:      domain.Condition(req.Condition),
		PriceMode:      domain.PriceMode(req.PriceMode),
		Currency:       req.Currency,
		Specifications: req.Specifications,
		Attachments:    resourceKeys(req.Attachments),
		Tags:           req.Tags,
		Variants:       variants,
	})
	if err != nil {
		return catalogapi.ListingDetail{}, err
	}
	// The images are resource ids held inline without a foreign key, so they are checked
	// here rather than by the database.
	if err := s.requireResources(ctx, l); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.CreateListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("create listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

func (s *Service) GetListing(ctx context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListing(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	// A listing that was never public is the seller's own draft. Hidden and soft-deleted stay
	// readable, because a cart or an order that names one still has to render.
	privileged := l.SellerID == req.ViewerID.Int64()
	if !privileged && (l.Status == domain.StatusDraft || l.Status == domain.StatusPending) {
		if err := s.requireModerator(ctx, req.ViewerID); err != nil {
			return catalogapi.ListingDetail{}, domain.ErrListingNotFound
		}
		privileged = true
	}
	return s.detailFor(ctx, l, req.ViewerID.Int64(), privileged)
}

// attachLocation copies a pickup address onto the listing: the one the seller chose from their own
// address book, or their default. A seller who has neither is refused by Publish, which is the
// point — a live listing with nowhere to collect from is one every checkout fails on, after the
// buyer has already chosen it.
//
// The address is read through the *actor*, so a seller can only ever name one of their own.
func (s *Service) attachLocation(ctx context.Context, l *domain.Listing, pickup *id.ID[id.Contact]) error {
	contact, err := s.pickupContact(ctx, l.SellerID, pickup)
	if err != nil {
		// No pickup address is this module's own refusal, with the wording a seller needs; a
		// contact read that failed for any other reason is the account module's to report.
		if errx.CodeOf(err) == accountapi.CodeNoPickupContact {
			return domain.ErrNoPickupAddress
		}
		return fmt.Errorf("read pickup contact: %w", err)
	}
	l.Location = &domain.Location{
		ProvinceCode: contact.ProvinceCode,
		ProvinceName: contact.ProvinceName,
		DistrictCode: contact.DistrictCode,
		DistrictName: contact.DistrictName,
		WardCode:     contact.WardCode,
		WardName:     contact.WardName,
		Latitude:     contact.Latitude,
		Longitude:    contact.Longitude,
	}
	return nil
}

// pickupContact is the chosen address or the default one. Named separately because the two reads
// are different questions of the account module — "this address of mine" and "my default".
func (s *Service) pickupContact(ctx context.Context, sellerID int64, pickup *id.ID[id.Contact]) (accountapi.Contact, error) {
	seller := id.Of[id.Account](sellerID)
	if pickup != nil {
		return s.accounts.GetContact(ctx, accountapi.GetContactRequest{ActorID: seller, ID: *pickup})
	}
	return s.accounts.GetPickupContact(ctx, accountapi.GetPickupContactRequest{AccountID: seller})
}

// requireSeller refuses a seller with no live verified identity document. The flag lives in
// the account module, which is also where the payout gate reads it.
func (s *Service) requireSeller(ctx context.Context, actorID id.ID[id.Account]) error {
	seller, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: actorID})
	if err != nil {
		return fmt.Errorf("read seller: %w", err)
	}
	if !seller.IdentityVerified {
		return domain.ErrIdentityRequired
	}
	return nil
}

// requireResources refuses an image id that is not a confirmed resource. A row pointing at
// nothing is a picture that never renders, and the seller should hear about it at upload
// time rather than from a buyer.
//
// A held edit's attachments are checked too: the row still carries the old ids while the edit
// waits, so validating the row alone would let an approval write ids that name nothing.
func (s *Service) requireResources(ctx context.Context, l *domain.Listing) error {
	wanted := append([]int64{}, l.Attachments...)
	for _, v := range l.Variants {
		wanted = append(wanted, v.Attachments...)
	}
	if l.PendingEdit != nil {
		wanted = append(wanted, l.PendingEdit.Attachments...)
	}
	if len(wanted) == 0 {
		return nil
	}
	found, err := s.resources(ctx, wanted)
	if err != nil {
		return err
	}
	for _, key := range wanted {
		if _, ok := found[key]; !ok {
			return domain.ErrAttachmentNotFound
		}
	}
	return nil
}

// resources resolves image ids in one query — a page of variants is not a query each — and each
// one comes back with a short-lived signed link on it. They are this module's own uploads: the
// upload that produced them was catalog's, and an id from another module resolves to nothing.
func (s *Service) resources(ctx context.Context, keys []int64) (map[int64]common.ResourceDTO, error) {
	return s.uploads.Resolve(ctx, keys)
}

func resourceKeys(ids []id.ID[id.Resource]) []int64 {
	out := make([]int64, 0, len(ids))
	for _, rid := range ids {
		out = append(out, rid.Int64())
	}
	return out
}

// detail is the seller's or a moderator's view: every command answers it, and only the owner
// or staff can issue one.
func (s *Service) detail(ctx context.Context, l *domain.Listing, viewerID int64) (catalogapi.ListingDetail, error) {
	return s.detailFor(ctx, l, viewerID, true)
}

// detailFor builds the product page: the aggregate plus the four things it does not own — the
// seller, the category, the images and the viewer's own wishlist state. A held edit is the
// owner's and staff's to see; a buyer gets the approved version until it is applied.
func (s *Service) detailFor(ctx context.Context, l *domain.Listing, viewerID int64, privileged bool) (catalogapi.ListingDetail, error) {
	seller, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
		ID: id.Of[id.Account](l.SellerID),
	})
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("read seller: %w", err)
	}
	category, err := s.category(ctx, l.CategoryID)
	if err != nil {
		return catalogapi.ListingDetail{}, err
	}
	keys := append([]int64{}, l.Attachments...)
	for _, v := range l.Variants {
		keys = append(keys, v.Attachments...)
	}
	images, err := s.resources(ctx, keys)
	if err != nil {
		return catalogapi.ListingDetail{}, err
	}
	favorited, err := s.repo.IsFavorited(ctx, viewerID, l.ID)
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("read favorited: %w", err)
	}
	favoriteCount, err := s.repo.CountFavorites(ctx, l.ID)
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("count favorites: %w", err)
	}

	out := catalogapi.ListingDetail{
		Location:       toAPILocation(l.Location, nil),
		ID:             id.Of[id.Listing](l.ID),
		Slug:           catalogapi.PublicSlug(id.Of[id.Listing](l.ID), l.Slug),
		Name:           l.Name,
		Description:    l.Description,
		Status:         string(l.Status),
		Condition:      string(l.Condition),
		PriceMode:      string(l.PriceMode),
		Currency:       l.Currency,
		Specifications: l.Specifications,
		Images:         pick(images, l.Attachments),
		Category:       toAPICategory(category),
		Tags:           l.Tags,
		TakenDownAt:    l.TakenDownAt,
		TakedownReason: l.TakedownReason,
		Sold:           l.CachedSold,
		Rating:         l.CachedRating,
		ReviewCount:    l.CachedReviewCount,
		Seller:         accountapi.AccountSummary{ID: seller.ID, Name: seller.Name, Avatar: seller.Avatar},
		Favorited:      favorited,
		FavoriteCount:  favoriteCount,
		CreatedAt:      l.CreatedAt,
		DeletedAt:      l.DeletedAt,
	}
	for _, v := range l.Variants {
		variant := catalogapi.Variant{
			ID:             id.Of[id.Variant](v.ID),
			Price:          v.Price,
			Attributes:     v.Attributes,
			PackageDetails: v.PackageDetails,
			Images:         pick(images, v.Attachments),
			IsFeatured:     v.IsFeatured,
			Stock: catalogapi.Stock{
				Quantity:  v.Stock.Quantity,
				Reserved:  v.Stock.Reserved,
				Sold:      v.Stock.Sold,
				Available: v.Stock.Available(),
			},
			CreatedAt: v.CreatedAt,
		}
		out.Variants = append(out.Variants, variant)
		if v.IsFeatured {
			out.FeaturedVariantID = new(variant.ID)
		}
	}
	if l.PendingEdit != nil && privileged {
		out.PendingEdit = toAPIPendingEdit(*l.PendingEdit)
	}
	return out, nil
}

// pick keeps the caller's order: the gallery's first image is the cover, so a map iteration
// would lose the one thing the array encodes.
func pick(found map[int64]common.ResourceDTO, keys []int64) []common.ResourceDTO {
	out := make([]common.ResourceDTO, 0, len(keys))
	for _, key := range keys {
		if res, ok := found[key]; ok {
			out = append(out, res)
		}
	}
	return out
}

// toAPILocation is the snapshot as a client reads it. nil in, nil out: a listing that was never
// published has no location, and a card renders that as "no area" rather than as empty strings.
func toAPILocation(area *domain.Location, distanceKM *float64) *catalogapi.ListingLocation {
	if area == nil {
		return nil
	}
	return &catalogapi.ListingLocation{
		ProvinceCode: area.ProvinceCode,
		ProvinceName: area.ProvinceName,
		DistrictCode: area.DistrictCode,
		DistrictName: area.DistrictName,
		WardCode:     area.WardCode,
		WardName:     area.WardName,
		DistanceKM:   distanceKM,
	}
}

func toAPIPendingEdit(e domain.PendingEdit) *catalogapi.PendingEdit {
	out := &catalogapi.PendingEdit{
		Name:           e.Name,
		Description:    e.Description,
		Specifications: e.Specifications,
		Tags:           e.Tags,
	}
	if e.CategoryID != nil {
		out.CategoryID = new(id.Of[id.Category](*e.CategoryID))
	}
	if e.Condition != nil {
		out.Condition = new(string(*e.Condition))
	}
	if e.PriceMode != nil {
		out.PriceMode = new(string(*e.PriceMode))
	}
	for _, key := range e.Attachments {
		out.Attachments = append(out.Attachments, id.Of[id.Resource](key))
	}
	return out
}

// UpdateListing applies the patch through the root, which decides whether it lands on the
// row or waits for a moderator.
func (s *Service) UpdateListing(ctx context.Context, req catalogapi.UpdateListingRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingForSeller(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	edit := domain.PendingEdit{
		Name:           req.Name,
		Description:    req.Description,
		Specifications: req.Specifications,
		Tags:           req.Tags,
	}
	if req.CategoryID != nil {
		edit.CategoryID = new(req.CategoryID.Int64())
	}
	if req.Condition != nil {
		edit.Condition = new(domain.Condition(*req.Condition))
	}
	if req.PriceMode != nil {
		edit.PriceMode = new(domain.PriceMode(*req.PriceMode))
	}
	if req.Attachments != nil {
		edit.Attachments = resourceKeys(req.Attachments)
	}
	if err := l.SubmitEdit(edit); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	// Featuring is not part of the reviewed set: which variant the card shows is not a claim
	// about what the listing is, so it takes effect at once.
	if req.FeaturedVariantID != nil {
		if err := l.SetFeatured(req.FeaturedVariantID.Int64()); err != nil {
			return catalogapi.ListingDetail{}, err
		}
	}
	if err := s.requireResources(ctx, l); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

// DeleteListing is a soft delete, refused while a checkout is in flight. A reservation exists
// exactly during that window, and it is a local fact — so no call into order, and no second
// definition of "open" to keep in step.
func (s *Service) DeleteListing(ctx context.Context, req catalogapi.DeleteListingRequest) error {
	l, err := s.repo.GetListingForSeller(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return fmt.Errorf("get listing: %w", err)
	}
	for _, v := range l.Variants {
		if v.Stock.Reserved > 0 {
			return domain.ErrListingInUse
		}
	}
	if err := s.repo.SoftDeleteListing(ctx, l.ID, req.ActorID.Int64(), req.ActorID.Int64()); err != nil {
		return fmt.Errorf("delete listing: %w", err)
	}
	return nil
}

func (s *Service) PublishListing(ctx context.Context, req catalogapi.PublishListingRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingForSeller(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	// Where the goods are, taken from the seller's pickup address as it stands now. Snapshotted
	// at publish rather than referenced: the address is in another schema, and a listing sold from
	// Hanoi should keep saying so after the seller moves. Re-publishing refreshes it.
	if err := s.attachLocation(ctx, l, req.PickupContactID); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := l.Publish(); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

func (s *Service) HideListing(ctx context.Context, req catalogapi.HideListingRequest) (catalogapi.ListingDetail, error) {
	l, err := s.repo.GetListingForSeller(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	if err := l.Hide(); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

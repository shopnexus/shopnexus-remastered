package catalog

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/id"
)

// AdminListListings is the moderation worklist, oldest first. It answers cards from a flat
// read model: a page of twenty must not be twenty aggregate loads, and nothing on this screen
// is editable in place.
func (s *Service) AdminListListings(ctx context.Context, req catalogapi.AdminListListingsRequest) (catalogapi.ListingPage, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return catalogapi.ListingPage{}, err
	}
	rows, total, err := s.repo.ListModerationQueue(ctx, port.QueueFilter{
		Status:   domain.Status(req.Status),
		SellerID: req.SellerID.Int64(),
		Offset:   offsetOf(req.Page, req.Limit),
		Limit:    req.Limit,
	})
	if err != nil {
		return catalogapi.ListingPage{}, fmt.Errorf("list moderation queue: %w", err)
	}
	cards, err := s.cards(ctx, rows)
	if err != nil {
		return catalogapi.ListingPage{}, err
	}
	return catalogapi.ListingPage{
		Data: cards,
		Meta: catalogapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// cards resolves the two things a summary row does not carry — the seller and the cover — in
// one call each for the whole page rather than one per card.
func (s *Service) cards(ctx context.Context, rows []port.ListingSummary) ([]catalogapi.Listing, error) {
	covers := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.CoverID != nil {
			covers = append(covers, *row.CoverID)
		}
	}
	images, err := s.resources(ctx, covers)
	if err != nil {
		return nil, err
	}
	out := make([]catalogapi.Listing, 0, len(rows))
	for _, row := range rows {
		card := catalogapi.Listing{
			ID:          id.Of[id.Listing](row.ID),
			Slug:        row.Slug,
			Name:        row.Name,
			Status:      string(row.Status),
			Condition:   string(row.Condition),
			PriceMode:   string(row.PriceMode),
			Currency:    row.Currency,
			Price:       row.Price,
			Sold:        row.Sold,
			Rating:      row.Rating,
			ReviewCount: row.ReviewCount,
			CategoryID:  id.Of[id.Category](row.CategoryID),
			Score:       row.Score,
			Location:    toAPILocation(row.Location, row.DistanceKM),
			DeletedAt:   row.DeletedAt,
			CreatedAt:   row.CreatedAt,
		}
		if row.CoverID != nil {
			if res, ok := images[*row.CoverID]; ok {
				card.Cover = &res
			}
		}
		// A moderator's queue shows whose listing it is; the seller lookup is per row because
		// a page holds a handful of distinct sellers and the account module has no batch read.
		seller, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
			ID: id.Of[id.Account](row.SellerID),
		})
		if err != nil {
			return nil, fmt.Errorf("read seller: %w", err)
		}
		card.Seller = accountapi.AccountSummary{ID: seller.ID, Name: seller.Name, Avatar: seller.Avatar}
		out = append(out, card)
	}
	return out, nil
}

// AdminApproveListing clears whatever was awaiting a decision. The root decides which of the
// two it was, so this reads the same for a first publication and for a held edit.
func (s *Service) AdminApproveListing(ctx context.Context, req catalogapi.ApproveListingRequest) (catalogapi.ListingDetail, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	l, err := s.repo.GetListing(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	if err := l.Approve(req.Note); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	// Approve may have written a held edit onto the row, so its attachments are checked here
	// rather than trusting what was validated when the edit was parked.
	if err := s.requireResources(ctx, l); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	return s.detail(ctx, l, req.ActorID.Int64())
}

// AdminTakedownListing removes a listing from the marketplace and records the reason.
// Suspending the seller as well is a separate decision and a separate call.
func (s *Service) AdminTakedownListing(ctx context.Context, req catalogapi.TakedownListingRequest) (catalogapi.ListingDetail, error) {
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	l, err := s.repo.GetListing(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("get listing: %w", err)
	}
	notify := req.NotifySeller == nil || *req.NotifySeller
	if err := l.Takedown(req.Reason, notify); err != nil {
		return catalogapi.ListingDetail{}, err
	}
	if err := s.repo.SaveListing(ctx, l, req.ActorID.Int64()); err != nil {
		return catalogapi.ListingDetail{}, fmt.Errorf("save listing: %w", err)
	}
	// Telling the seller is a notification the account module owns, and this slice has no seam
	// to it: the choice lands in the trail, not in an outbox. Wire it when that API is a
	// dependency rather than a plan.
	return s.detail(ctx, l, req.ActorID.Int64())
}

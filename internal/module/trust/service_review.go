package trust

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/id"
)

// ListReviews is the public product page. Reading is anonymous, but a caller who is signed
// in gets their own vote back on each row, so the page can render the button they pressed.
func (s *Service) ListReviews(ctx context.Context, req trustapi.ListReviewsRequest) (trustapi.ReviewPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.ReviewPage{}, err
	}
	listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
		ID: req.ListingID, ViewerID: req.ViewerID,
	})
	if err != nil {
		return trustapi.ReviewPage{}, fmt.Errorf("read listing: %w", err)
	}
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.ReviewPage{}, err
	}
	rows, err := s.repo.ListReviews(ctx, port.ReviewFilter{
		ListingID: req.ListingID.Int64(), Rating: req.Rating, Sort: req.Sort, Cursor: cursor,
	})
	if err != nil {
		return trustapi.ReviewPage{}, fmt.Errorf("list reviews: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, func(v domain.Review) time.Time { return v.CreatedAt })
	data, err := s.reviewViews(ctx, rows, listing.Seller.ID.Int64(), req.ViewerID, repliesOnAPage)
	if err != nil {
		return trustapi.ReviewPage{}, err
	}
	return trustapi.ReviewPage{Data: data, Meta: meta}, nil
}

// SubmitReview writes a buyer's rating of something they bought. The order is what earns it:
// no purchase, no review — and one review per (listing, order), so buying twice earns a
// second one but one purchase does not earn two.
func (s *Service) SubmitReview(ctx context.Context, req trustapi.SubmitReviewRequest) (trustapi.Review, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Review{}, err
	}
	order, err := s.orders.GetOrder(ctx, orderapi.OrderRequest{ActorID: req.ActorID, ID: req.OrderID})
	if err != nil {
		return trustapi.Review{}, fmt.Errorf("read order: %w", err)
	}
	// Only the buyer reviews the goods, and only once the sale is over.
	if order.Buyer.ID != req.ActorID {
		return trustapi.Review{}, domain.ErrNotAParty
	}
	if order.State == "open" {
		return trustapi.Review{}, domain.ErrOrderNotFinished
	}
	if !covers(order, req.ListingID) {
		return trustapi.Review{}, domain.ErrListingNotInOrder
	}
	attachments := rawIDs(req.Attachments)
	if err := s.requireResources(ctx, attachments); err != nil {
		return trustapi.Review{}, err
	}
	v, err := domain.NewReview(req.ListingID.Int64(), req.OrderID.Int64(), req.ActorID.Int64(),
		req.Rating, req.Body, attachments)
	if err != nil {
		return trustapi.Review{}, err
	}
	sellerID := order.Seller.ID.Int64()
	if err := s.repo.InsertReview(ctx, &v, sellerID); err != nil {
		return trustapi.Review{}, fmt.Errorf("insert review: %w", err)
	}
	s.syncListingRating(ctx, v.ListingID)
	return s.reviewView(ctx, v, sellerID, req.ActorID, 0)
}

// GetReview carries the whole reply thread, unlike the listing page, which caps it.
func (s *Service) GetReview(ctx context.Context, req trustapi.GetReviewRequest) (trustapi.Review, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Review{}, err
	}
	v, err := s.repo.FindReview(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Review{}, fmt.Errorf("find review: %w", err)
	}
	sellerID, err := s.listingSeller(ctx, v.ListingID, req.ViewerID)
	if err != nil {
		return trustapi.Review{}, err
	}
	return s.reviewView(ctx, v, sellerID, req.ViewerID, 0)
}

// UpdateReview is the author's own edit. The rating moves the seller's aggregate by the
// difference in the same transaction — a 5 rewritten to a 1 that leaves the average alone is
// a number nobody can reproduce.
func (s *Service) UpdateReview(ctx context.Context, req trustapi.UpdateReviewRequest) (trustapi.Review, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Review{}, err
	}
	v, err := s.repo.FindReview(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Review{}, fmt.Errorf("find review: %w", err)
	}
	// Editing is the author's alone: a moderator may remove a review, not rewrite one.
	if !v.MutableBy(req.ActorID.Int64()) {
		return trustapi.Review{}, domain.ErrReviewForbidden
	}
	previousRating := v.Rating
	if req.Rating != nil {
		if err := v.SetRating(*req.Rating); err != nil {
			return trustapi.Review{}, err
		}
	}
	if req.Body != nil {
		if err := v.SetBody(*req.Body); err != nil {
			return trustapi.Review{}, err
		}
	}
	if req.Attachments != nil {
		attachments := rawIDs(*req.Attachments)
		if err := s.requireResources(ctx, attachments); err != nil {
			return trustapi.Review{}, err
		}
		v.SetAttachments(attachments)
	}
	sellerID, err := s.listingSeller(ctx, v.ListingID, req.ActorID)
	if err != nil {
		return trustapi.Review{}, err
	}
	delta := int64(v.Rating) - int64(previousRating)
	if err := s.repo.SaveReview(ctx, v, sellerID, delta); err != nil {
		return trustapi.Review{}, fmt.Errorf("save review: %w", err)
	}
	if delta != 0 {
		s.syncListingRating(ctx, v.ListingID)
	}
	return s.reviewView(ctx, v, sellerID, req.ActorID, 0)
}

// DeleteReview is the author's, or a moderator's acting on an upheld report. Removal drops
// the rating out of the seller's reputation too, so the cached listing rating follows.
func (s *Service) DeleteReview(ctx context.Context, req trustapi.ReviewRequest) error {
	if err := s.v.Struct(req); err != nil {
		return err
	}
	v, err := s.repo.FindReview(ctx, req.ID.Int64())
	if err != nil {
		return fmt.Errorf("find review: %w", err)
	}
	if !v.MutableBy(req.ActorID.Int64()) && !s.isModerator(ctx, req.ActorID) {
		return domain.ErrReviewForbidden
	}
	sellerID, err := s.listingSeller(ctx, v.ListingID, req.ActorID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteReview(ctx, v.ID, sellerID, v.Rating); err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	s.syncListingRating(ctx, v.ListingID)
	return nil
}

// SubmitReply adds to a review's thread — mainly the seller answering, though anyone may. No
// per-thread limit: a conversation that stops after one reply each is not a conversation.
func (s *Service) SubmitReply(ctx context.Context, req trustapi.SubmitReplyRequest) (trustapi.ReviewReply, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.ReviewReply{}, err
	}
	v, err := s.repo.FindReview(ctx, req.ReviewID.Int64())
	if err != nil {
		return trustapi.ReviewReply{}, fmt.Errorf("find review: %w", err)
	}
	reply, err := domain.NewReviewReply(v.ID, req.ActorID.Int64(), req.Body)
	if err != nil {
		return trustapi.ReviewReply{}, err
	}
	if err := s.repo.InsertReply(ctx, &reply); err != nil {
		return trustapi.ReviewReply{}, fmt.Errorf("insert reply: %w", err)
	}
	sellerID, err := s.listingSeller(ctx, v.ListingID, req.ActorID)
	if err != nil {
		return trustapi.ReviewReply{}, err
	}
	author, err := s.summary(ctx, reply.AuthorID)
	if err != nil {
		return trustapi.ReviewReply{}, err
	}
	return toAPIReply(reply, author, sellerID), nil
}

// DeleteReply is the author's, or a moderator's acting on an upheld report.
func (s *Service) DeleteReply(ctx context.Context, req trustapi.ReplyRequest) error {
	if err := s.v.Struct(req); err != nil {
		return err
	}
	reply, err := s.repo.FindReply(ctx, req.ID.Int64())
	if err != nil {
		return fmt.Errorf("find reply: %w", err)
	}
	if reply.AuthorID != req.ActorID.Int64() && !s.isModerator(ctx, req.ActorID) {
		return domain.ErrReplyForbidden
	}
	if err := s.repo.DeleteReply(ctx, reply.ID); err != nil {
		return fmt.Errorf("delete reply: %w", err)
	}
	return nil
}

// VoteReview records one vote per account per review, replaced in place. Voting on your own
// review is refused — a tally an author can inflate says nothing about helpfulness.
func (s *Service) VoteReview(ctx context.Context, req trustapi.VoteReviewRequest) (trustapi.VoteTally, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.VoteTally{}, err
	}
	v, err := s.repo.FindReview(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.VoteTally{}, fmt.Errorf("find review: %w", err)
	}
	if v.AuthorID == req.ActorID.Int64() {
		return trustapi.VoteTally{}, domain.ErrSelfVote
	}
	vote, err := domain.NewReviewVote(v.ID, req.ActorID.Int64(), req.Vote)
	if err != nil {
		return trustapi.VoteTally{}, err
	}
	tally, err := s.repo.PutVote(ctx, vote)
	if err != nil {
		return trustapi.VoteTally{}, fmt.Errorf("put vote: %w", err)
	}
	return toAPITally(tally), nil
}

// UnvoteReview withdraws a vote by removing the row rather than storing a neutral one.
func (s *Service) UnvoteReview(ctx context.Context, req trustapi.ReviewRequest) (trustapi.VoteTally, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.VoteTally{}, err
	}
	tally, err := s.repo.DeleteVote(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return trustapi.VoteTally{}, fmt.Errorf("delete vote: %w", err)
	}
	return toAPITally(tally), nil
}

// covers reports whether an order carried this listing. A review of something the order did
// not include is not a review of a purchase.
func covers(order orderapi.Order, listingID id.ID[id.Listing]) bool {
	for _, item := range order.Items {
		if item.ListingID == listingID && item.CancelledAt == nil {
			return true
		}
	}
	return false
}

// listingSeller answers who owns the listing, which is what marks a reply as the seller's.
// A listing that no longer exists is not an error here: its reviews outlive it, and the
// answer is only used for decoration.
func (s *Service) listingSeller(ctx context.Context, listingID int64, viewerID id.ID[id.Account]) (int64, error) {
	listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
		ID: id.Of[id.Listing](listingID), ViewerID: viewerID,
	})
	if err != nil {
		s.log.Debug("resolve listing seller failed", "listing_id", listingID, "err", err)
		return 0, nil
	}
	return listing.Seller.ID.Int64(), nil
}

// syncListingRating pushes the recomputed average into catalog's cache. Best-effort: the
// review is written and counted, and a cache that lags is repaired by the next write —
// failing the request would refuse a review over a denormalized number.
func (s *Service) syncListingRating(ctx context.Context, listingID int64) {
	average, count, err := s.repo.ReviewAverage(ctx, listingID)
	if err != nil {
		s.log.Error("read review average", "listing_id", listingID, "err", err)
		return
	}
	err = s.catalog.SyncListingRating(ctx, catalogapi.SyncListingRatingRequest{
		ListingID: id.Of[id.Listing](listingID), Rating: average, Count: count,
	})
	if err != nil {
		s.log.Error("sync listing rating", "listing_id", listingID, "err", err)
	}
}

// reviewViews renders a page: the authors, the attachments, the capped threads and the
// caller's own votes, each resolved once for the whole page rather than per row.
func (s *Service) reviewViews(ctx context.Context, rows []domain.Review, sellerID int64,
	viewerID id.ID[id.Account], replyLimit int) ([]trustapi.Review, error) {
	if len(rows) == 0 {
		return []trustapi.Review{}, nil
	}
	reviewIDs := make([]int64, 0, len(rows))
	authorIDs := make([]int64, 0, len(rows))
	keys := make([]int64, 0, len(rows))
	for _, row := range rows {
		reviewIDs = append(reviewIDs, row.ID)
		authorIDs = append(authorIDs, row.AuthorID)
		keys = append(keys, row.Attachments...)
	}
	threads, err := s.repo.ListReplies(ctx, reviewIDs, replyLimit)
	if err != nil {
		return nil, fmt.Errorf("list replies: %w", err)
	}
	for _, thread := range threads {
		for _, reply := range thread {
			authorIDs = append(authorIDs, reply.AuthorID)
		}
	}
	votes, err := s.repo.MyVotes(ctx, viewerID.Int64(), reviewIDs)
	if err != nil {
		return nil, fmt.Errorf("read my votes: %w", err)
	}
	found, err := s.resources(ctx, keys)
	if err != nil {
		return nil, err
	}
	names := s.summaries(ctx, authorIDs)

	out := make([]trustapi.Review, 0, len(rows))
	for _, row := range rows {
		replies := make([]trustapi.ReviewReply, 0, len(threads[row.ID]))
		for _, reply := range threads[row.ID] {
			replies = append(replies, toAPIReply(reply, names[reply.AuthorID], sellerID))
		}
		var myVote *int16
		if vote, ok := votes[row.ID]; ok {
			myVote = &vote
		}
		out = append(out, trustapi.Review{
			ID:          id.Of[id.Review](row.ID),
			ListingID:   id.Of[id.Listing](row.ListingID),
			Author:      names[row.AuthorID],
			Rating:      row.Rating,
			Body:        row.Body,
			Attachments: pick(found, row.Attachments),
			Replies:     replies,
			ReplyCount:  row.ReplyCount,
			Votes: trustapi.VoteTally{
				Helpful: row.HelpfulCount, NotHelpful: row.NotHelpfulCount, MyVote: myVote,
			},
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) reviewView(ctx context.Context, v domain.Review, sellerID int64,
	viewerID id.ID[id.Account], replyLimit int) (trustapi.Review, error) {
	views, err := s.reviewViews(ctx, []domain.Review{v}, sellerID, viewerID, replyLimit)
	if err != nil {
		return trustapi.Review{}, err
	}
	return views[0], nil
}

func toAPIReply(r domain.ReviewReply, author accountapi.AccountSummary, sellerID int64) trustapi.ReviewReply {
	return trustapi.ReviewReply{
		ID:        id.Of[id.ReviewReply](r.ID),
		Author:    author,
		IsSeller:  sellerID != 0 && r.AuthorID == sellerID,
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
	}
}

func toAPITally(t port.VoteTally) trustapi.VoteTally {
	return trustapi.VoteTally{Helpful: t.Helpful, NotHelpful: t.NotHelpful, MyVote: t.MyVote}
}

package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/id"
)

// The home page as shelves: several short rows, each carrying the reason it is there, instead of
// one ranked page that carries none.
//
// The interesting half is `interest`. A personalised feed already ranks against the account's
// four interest slots — and blends them, so the reader gets one page and no way to tell which of
// their tastes produced which card. Here each slot is its own row. Nothing new is computed for
// it: the slots exist, the retrieval is the same single-probe relevance search `similar_to` uses,
// and the only thing added is a *name* for each slot, which is the nearest category to its
// vector — the same ranking `GET /categories?near=` already answers with.
//
// An interest has no name of its own because it is a direction in embedding space; naming it by
// its neighbourhood is the honest label, and it is why this endpoint exists at all rather than
// the client firing six `/listings` calls and inventing the titles itself. The slots are not
// published and never will be — a vector of the account's behaviour is not a thing to hand out.

// shelfMinCards is the shortest row worth being a row. A rail with two cards in it reads as a
// failed request rather than as a recommendation, and the shelf is dropped instead.
const shelfMinCards = 3

// namesPerInterest is how many candidate names each slot is offered. Two slots regularly land in
// the same neighbourhood — somebody looking at phones and at phone cases — and two rows with the
// same title is one row that looks broken, so the second slot takes its next-best name.
const namesPerInterest = 3

// viewedAnchors is how far back the "because you looked at this" row reads. One row, but the most
// recent action may be against a listing that has since been taken down or that the shopper has
// hidden, so there are a few to fall through.
const viewedAnchors = 5

func (s *Service) ListShelves(ctx context.Context, req catalogapi.ListShelvesRequest) (catalogapi.ShelfList, error) {
	if err := s.v.Struct(req); err != nil {
		return catalogapi.ShelfList{}, err
	}
	b := &shelves{svc: s, viewer: req.ViewerID.Int64(), limit: req.Limit}

	// The reader's own shelves first, then the marketplace's. A home page that opens on "trending"
	// for somebody it knows things about is a home page that learned nothing.
	if b.viewer != 0 {
		b.becauseYouViewed(ctx)
		b.interests(ctx)
	}
	b.platform(ctx, catalogapi.ReasonTrending, port.SortTrending)
	b.platform(ctx, catalogapi.ReasonBestSelling, port.SortBestSelling)
	b.platform(ctx, catalogapi.ReasonTopRated, port.SortRating)
	b.platform(ctx, catalogapi.ReasonNewest, port.SortNewest)

	// One shelf failing is not the page failing — the point of several rows is that they are
	// independent. Everything failing is, though: an empty page and a broken one look the same to
	// a reader, and only the error says which it was.
	if len(b.out) == 0 && b.lastErr != nil {
		return catalogapi.ShelfList{}, b.lastErr
	}
	return catalogapi.ShelfList{Data: b.out}, nil
}

// shelves accumulates the page. `seen` is what makes the rows feel like different rows: a listing
// already shown is excluded from every shelf after it, so "vì bạn đã xem" and "đang là xu hướng"
// cannot be the same twelve cards under two headings. Shelf order therefore decides who keeps a
// contested listing, which is why the personal ones are built first.
type shelves struct {
	svc    *Service
	viewer int64
	limit  int

	out     []catalogapi.Shelf
	seen    []int64
	names   map[int64]bool
	lastErr error
}

// becauseYouViewed is the most recent listing the shopper opened, and the neighbourhood of it.
//
// A view is the only signal worth saying out loud: a favourite is already on their wishlist and a
// purchase is already theirs, so neither is news. `similar_to` on the browse is the same
// primitive, so the "see all" link widens into exactly this row.
func (b *shelves) becauseYouViewed(ctx context.Context) {
	signals, err := b.svc.repo.RecentSignals(ctx, b.viewer,
		[]string{catalogapi.InteractionView, catalogapi.InteractionClickFromSearch}, viewedAnchors)
	if err != nil {
		b.fail("read recent signals", err)
		return
	}
	for _, signal := range signals {
		anchor, err := b.svc.repo.ListListingsByIDs(ctx, []int64{signal.ListingID})
		if err != nil {
			b.fail("read viewed listing", err)
			return
		}
		// A listing taken down, deleted or hidden since it was looked at is not something to
		// build a row about, and ListListingsByIDs answers only the live ones.
		if len(anchor) == 0 {
			continue
		}
		filter := b.filter()
		filter.ExcludeIDs = append(filter.ExcludeIDs, signal.ListingID)
		probe, err := b.svc.repo.ListingProbe(ctx, signal.ListingID)
		if errors.Is(err, domain.ErrListingNotEmbedded) {
			continue // nothing to rank against; the next anchor may have a vector
		}
		if err != nil {
			b.fail("read listing probe", err)
			return
		}
		filter.Sort = port.SortRelevance
		filter.Terms = []port.Term{{Weight: 1, Probe: &probe}}

		listingID := id.Of[id.Listing](signal.ListingID).String()
		b.add(ctx, filter, catalogapi.Shelf{
			Key:    "because-you-viewed:" + listingID,
			Reason: catalogapi.ReasonBecauseYouViewed,
			Subject: &catalogapi.ShelfSubject{
				Kind: catalogapi.SubjectListing, ID: listingID, Name: anchor[0].Name,
			},
			Browse: catalogapi.ShelfBrowse{SimilarTo: &listingID},
		})
		// The anchor is on the page too — as the row's own subject — so a later shelf offering
		// it as a card would be offering the reader back the listing this row is *about*.
		b.seen = append(b.seen, signal.ListingID)
		return
	}
}

// interests is the four slots, shown apart.
//
// Each row is the personalised feed restricted to one slot: the same retrieval `sort=recommended`
// runs, handed a single interest instead of four. Not the relevance search `similar_to` uses,
// which was the first thing tried and answered one card per shelf — that statement's per-leg
// relevance floor is calibrated for a probe of somebody's *words*, and an interest is an average
// of listing vectors, so its nearest neighbour sits far above the rest and the floor cuts at the
// cliff. The recommended statement was written for these vectors and has no floor: it draws a pool
// several pages deep and samples it.
//
// The seed is derived from the account and the slot rather than the clock, so a shelf holds still
// between visits — a home page that reshuffles under the reader is a home page they cannot come
// back to — and so each slot keeps its own entry in the feed cache.
//
// The slot order is the interest strength, so the taste the account's behaviour points at hardest
// is the row nearest the top.
func (b *shelves) interests(ctx context.Context) {
	interests, err := b.svc.repo.Interests(ctx, b.viewer)
	if err != nil {
		b.fail("read interests", err)
		return
	}
	for slot, interest := range interests {
		if len(interest.Vector) == 0 {
			continue
		}
		subject, err := b.nameOf(ctx, interest.Vector)
		if err != nil {
			b.fail("name interest", err)
			return
		}
		// A slot nothing in the category tree is near is a slot with no honest label, and a row
		// titled "Gợi ý cho bạn" four times is four rows that say nothing. It stays in the
		// blended feed, where it needs no name.
		if subject == nil {
			b.svc.log.Info("interest unnamed", "slot", slot)
			continue
		}
		filter := b.filter()
		filter.Sort = port.SortRecommended
		filter.Interests = []port.Interest{{Vector: interest.Vector, Weight: 1}}
		filter.Seed = fmt.Sprintf("shelf:%d:%d", b.viewer, slot)
		// A row named after a taste has to be about that taste. The blended feed mixes in a
		// fifth of whatever is new precisely to keep itself from closing in, but here the
		// interest floor can leave a slot matching nothing at all — and then the fresh source
		// would fill the row anyway, putting twelve unrelated new arrivals under a heading
		// naming a taste. Without the fresh cards the row is simply short, and shelfMinCards
		// drops it.
		filter.MatchedOnly = true

		b.add(ctx, filter, catalogapi.Shelf{
			Key:     "interest:" + strconv.Itoa(slot),
			Reason:  catalogapi.ReasonInterest,
			Subject: subject,
			// The category the slot was named by is the honest widening: there is no public
			// "rank against my third interest" and there should not be one.
			Browse: catalogapi.ShelfBrowse{Sort: port.SortNewest, CategoryID: &subject.ID},
		})
	}
}

// nameOf labels an interest by the nearest category to it, skipping any name already used by an
// earlier slot. Nil when every candidate is taken or the tree has nothing near it.
func (b *shelves) nameOf(ctx context.Context, v port.Vector) (*catalogapi.ShelfSubject, error) {
	near, err := b.svc.repo.NearestCategories(ctx, []port.Vector{v}, namesPerInterest)
	if err != nil {
		return nil, fmt.Errorf("nearest categories: %w", err)
	}
	if b.names == nil {
		b.names = map[int64]bool{}
	}
	for _, scored := range near {
		if b.names[scored.Category.ID] {
			continue
		}
		b.names[scored.Category.ID] = true
		return &catalogapi.ShelfSubject{
			Kind: catalogapi.SubjectCategory,
			ID:   id.Of[id.Category](scored.Category.ID).String(),
			Name: scored.Category.Name,
		}, nil
	}
	return nil, nil
}

// platform is a shelf about the marketplace rather than about the reader: one sort, no subject.
func (b *shelves) platform(ctx context.Context, reason catalogapi.ShelfReason, sort string) {
	filter := b.filter()
	filter.Sort = sort
	b.add(ctx, filter, catalogapi.Shelf{
		Key:    reason,
		Reason: reason,
		Browse: catalogapi.ShelfBrowse{Sort: sort},
	})
}

// filter is the shelf baseline: live listings, one row's worth, and none of what the rows above
// already showed.
func (b *shelves) filter() port.ListingFilter {
	return port.ListingFilter{
		ViewerID:   b.viewer,
		ExcludeIDs: b.seen,
		Limit:      b.limit,
		// `add` throws the count away — a shelf is a row of cards, not a paged list — and at a
		// million listings each one cost about four seconds to compute. Three shelves on the
		// anonymous home page made it a fourteen-second request.
		SkipTotal: true,
	}
}

// add runs the shelf's retrieval and keeps the row if it came back with enough to be a row.
func (b *shelves) add(ctx context.Context, filter port.ListingFilter, shelf catalogapi.Shelf) {
	rows, _, err := b.svc.listingRows(ctx, filter)
	if err != nil {
		b.fail("retrieve shelf", err, "shelf", shelf.Key)
		return
	}
	if len(rows) < shelfMinCards {
		// Logged, not silent: "the shelf is missing" and "the shelf came back short" look the
		// same to a reader, and only this line says which.
		b.svc.log.Info("shelf too short", "shelf", shelf.Key, "rows", len(rows))
		return
	}
	cards, err := b.svc.listingCards(ctx, rows, b.viewer)
	if err != nil {
		b.fail("project shelf", err, "shelf", shelf.Key)
		return
	}
	shelf.Listings = cards
	b.out = append(b.out, shelf)
	for _, row := range rows {
		b.seen = append(b.seen, row.ID)
	}
}

// fail records a shelf that could not be built and moves on. Logged rather than returned, because
// the reader loses a row instead of the page — and remembered, because a page that lost every row
// has to answer with the reason rather than with an empty list.
func (b *shelves) fail(what string, err error, attrs ...any) {
	b.lastErr = fmt.Errorf("%s: %w", what, err)
	b.svc.log.Warn("shelf skipped", append([]any{"what", what, "err", err}, attrs...)...)
}

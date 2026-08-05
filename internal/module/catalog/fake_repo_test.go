package catalog_test

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/provider/storage"
)

// fakeRepo is an in-memory port.Repository. It enforces the constraints the schema does —
// the unique name, the cycle guard, RESTRICT on a category in use — because those are the
// ones the service's behaviour is built on top of.
type fakeRepo struct {
	nextID     int64
	categories map[int64]domain.Category
	// inUse marks a category a listing references, which the fake cannot derive because
	// listings are not in this slice.
	inUse map[int64]bool
	tags  map[string]domain.Tag
	// The vector maps stand in for the embedding pass, which nothing in this module runs: a
	// seed absent from them is one that has not been embedded yet.
	tagVectors      map[string]port.Vector
	categoryVectors map[int64]port.Vector

	// The listing aggregate, stored flat the way the tables are: the root, its variants and
	// its tag joins are separate so a Save that forgets one is visible here too.
	listings []storedListing
	stock    map[int64]domain.Stock
	// variantListing is the join CommitStock walks to bump the listing's counter.
	variantListing map[int64]int64
	// movements is CommitStock/UncommitStock's idempotency key set, mirroring the
	// adapter's stock_movement table: a key already claimed makes the call a no-op
	// instead of moving the counters again.
	movements map[string]bool
	favorites map[[2]int64]bool
	// interests are the account's slots, which is what sort=recommended ranks against.
	interests map[int64][]port.Vector
	// resources is this module's own resource table: an id absent from it names no confirmed
	// upload, which is what ErrAttachmentNotFound is about.
	resources map[int64]bool
	// audit is the trail Save writes in the same transaction as the change.
	audit []common.AuditEntry
}

// storedListing is the row plus its children, cloned in and out so a caller that never saves
// cannot change what is stored — the same isolation a database read gives.
type storedListing struct {
	listing  domain.Listing
	variants []domain.Variant
	tags     []string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		categories: map[int64]domain.Category{},
		inUse:      map[int64]bool{},
		tags:       map[string]domain.Tag{},

		tagVectors:      map[string]port.Vector{},
		categoryVectors: map[int64]port.Vector{},

		stock:          map[int64]domain.Stock{},
		variantListing: map[int64]int64{},
		movements:      map[string]bool{},
		favorites:      map[[2]int64]bool{},
		interests:      map[int64][]port.Vector{},
		resources:      map[int64]bool{},
	}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

var _ port.Repository = (*fakeRepo)(nil)

func (f *fakeRepo) ListCategories(context.Context) ([]domain.Category, error) {
	out := make([]domain.Category, 0, len(f.categories))
	for _, c := range f.categories {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b domain.Category) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeRepo) CreateCategory(_ context.Context, c *domain.Category) error {
	if f.nameTaken(c.Name, 0) {
		return domain.ErrCategoryNameTaken
	}
	if c.ParentID != nil {
		if _, ok := f.categories[*c.ParentID]; !ok {
			return domain.ErrCategoryNotFound
		}
	}
	c.ID = f.id()
	f.categories[c.ID] = *c
	return nil
}

func (f *fakeRepo) UpdateCategory(_ context.Context, c domain.Category) error {
	if _, ok := f.categories[c.ID]; !ok {
		return domain.ErrCategoryNotFound
	}
	if f.nameTaken(c.Name, c.ID) {
		return domain.ErrCategoryNameTaken
	}
	if c.ParentID != nil {
		if _, ok := f.categories[*c.ParentID]; !ok {
			return domain.ErrCategoryNotFound
		}
		if f.isDescendant(*c.ParentID, c.ID) {
			return domain.ErrCategoryCycle
		}
	}
	f.categories[c.ID] = c
	return nil
}

func (f *fakeRepo) DeleteCategory(_ context.Context, id int64) error {
	if _, ok := f.categories[id]; !ok {
		return domain.ErrCategoryNotFound
	}
	if f.inUse[id] {
		return domain.ErrCategoryInUse
	}
	// ON DELETE SET NULL: children are promoted to roots.
	for childID, child := range f.categories {
		if child.ParentID != nil && *child.ParentID == id {
			child.ParentID = nil
			f.categories[childID] = child
		}
	}
	delete(f.categories, id)
	return nil
}

func (f *fakeRepo) nameTaken(name string, self int64) bool {
	for id, c := range f.categories {
		if id != self && c.Name == name {
			return true
		}
	}
	return false
}

// isDescendant walks up from candidate: reaching root means it is not under id.
func (f *fakeRepo) isDescendant(candidate, id int64) bool {
	for at := candidate; ; {
		if at == id {
			return true
		}
		c, ok := f.categories[at]
		if !ok || c.ParentID == nil {
			return false
		}
		at = *c.ParentID
	}
}

// --- tags ---

func (f *fakeRepo) ListTags(_ context.Context, filter port.TagFilter) ([]domain.Tag, int64, error) {
	var matched []domain.Tag
	for _, t := range f.tags {
		if filter.Prefix != "" && !strings.HasPrefix(t.Slug, filter.Prefix) {
			continue
		}
		matched = append(matched, t)
	}
	slices.SortFunc(matched, func(a, b domain.Tag) int { return strings.Compare(a.Slug, b.Slug) })
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

// PutTag is an upsert, as the ON CONFLICT in the adapter is.
func (f *fakeRepo) PutTag(_ context.Context, t domain.Tag) error {
	f.tags[t.Slug] = t
	return nil
}

func (f *fakeRepo) DeleteTag(_ context.Context, slug string) error {
	if _, ok := f.tags[slug]; !ok {
		return domain.ErrTagNotFound
	}
	delete(f.tags, slug)
	return nil
}

// --- semantic ---

// The fake ranks by dot product on the tiny vectors a test writes, which orders the same way
// cosine distance does for the unit-ish vectors used there.
func (f *fakeRepo) SeedVectors(_ context.Context, seeds []port.Seed) ([]port.Vector, error) {
	// One entry per seed, nil where the embedding pass has not written one — the adapter's
	// contract, and the reason a missing seed can be named.
	out := make([]port.Vector, len(seeds))
	for i, s := range seeds {
		if s.TagSlug != "" {
			out[i] = f.tagVectors[s.TagSlug]
			continue
		}
		out[i] = f.categoryVectors[s.CategoryID]
	}
	return out, nil
}

func (f *fakeRepo) NearestCategories(_ context.Context, vectors []port.Vector, limit int) ([]port.ScoredCategory, error) {
	if len(vectors) == 0 {
		return nil, nil
	}
	probe := mean(vectors)
	var out []port.ScoredCategory
	for id, v := range f.categoryVectors {
		c, ok := f.categories[id]
		if !ok {
			continue // the adapter JOINs, so an orphan embedding is not a row
		}
		out = append(out, port.ScoredCategory{Category: c, Score: dot(probe, v)})
	}
	slices.SortFunc(out, func(a, b port.ScoredCategory) int { return cmp.Compare(b.Score, a.Score) })
	return out[:min(limit, len(out))], nil
}

func (f *fakeRepo) NearestTags(_ context.Context, vectors []port.Vector, exclude []string, offset, limit int) ([]port.ScoredTag, error) {
	if len(vectors) == 0 {
		return nil, nil
	}
	probe := mean(vectors)
	var out []port.ScoredTag
	for slug, v := range f.tagVectors {
		if slices.Contains(exclude, slug) {
			continue
		}
		t, ok := f.tags[slug]
		if !ok {
			continue // as above: an embedding with no tag row is not a result
		}
		out = append(out, port.ScoredTag{Tag: t, Score: dot(probe, v)})
	}
	slices.SortFunc(out, func(a, b port.ScoredTag) int { return cmp.Compare(b.Score, a.Score) })
	if offset >= len(out) {
		return nil, nil
	}
	return out[offset:min(offset+limit, len(out))], nil
}

func mean(vectors []port.Vector) port.Vector {
	sum := make(port.Vector, len(vectors[0]))
	for _, v := range vectors {
		for i := range sum {
			sum[i] += v[i]
		}
	}
	for i := range sum {
		sum[i] /= float32(len(vectors))
	}
	return sum
}

func dot(a, b port.Vector) float64 {
	var total float64
	for i := range a {
		total += float64(a[i] * b[i])
	}
	return total
}

// --- stock ---

func (f *fakeRepo) FindStock(_ context.Context, variantID int64) (domain.Stock, error) {
	s, ok := f.stock[variantID]
	if !ok {
		return domain.Stock{}, domain.ErrVariantNotFound
	}
	return s, nil
}

func (f *fakeRepo) ReserveStock(_ context.Context, variantID, units int64) error {
	s, ok := f.stock[variantID]
	if !ok || units <= 0 || s.Committed()+units > s.Quantity {
		return domain.ErrInsufficientStock
	}
	s.Reserved += units
	f.stock[variantID] = s
	return nil
}

func (f *fakeRepo) ReleaseStock(_ context.Context, variantID, units int64) error {
	s, ok := f.stock[variantID]
	if !ok || units <= 0 || s.Reserved < units {
		return domain.ErrInsufficientStock
	}
	s.Reserved -= units
	f.stock[variantID] = s
	return nil
}

func (f *fakeRepo) CommitStock(_ context.Context, variantID, units int64, key string) error {
	if units <= 0 {
		return domain.ErrInsufficientStock
	}
	if key == "" {
		return domain.ErrStockMovementKeyRequired
	}
	if f.movements[key] {
		// Already applied — the caller asked for the same effect, and it is there.
		return nil
	}
	s, ok := f.stock[variantID]
	if !ok || s.Reserved < units {
		return domain.ErrInsufficientStock
	}
	s.Reserved -= units
	s.Sold += units
	f.stock[variantID] = s
	f.movements[key] = true
	// The listing's counter moves with the sale, as the adapter does in one transaction.
	if listingID, ok := f.variantListing[variantID]; ok {
		if at := f.listingAt(listingID); at >= 0 {
			f.listings[at].listing.CachedSold += units
		}
	}
	return nil
}

// UncommitStock is CommitStock's reversal — a cancelled or refunded order putting sold
// units back on the shelf. Never ReleaseStock: by the time a sale exists, `reserved`
// holds none of these units anymore.
func (f *fakeRepo) UncommitStock(_ context.Context, variantID, units int64, key string) error {
	if units <= 0 {
		return domain.ErrInsufficientStock
	}
	if key == "" {
		return domain.ErrStockMovementKeyRequired
	}
	if f.movements[key] {
		return nil
	}
	s, ok := f.stock[variantID]
	if !ok || s.Sold < units {
		return domain.ErrInsufficientStock
	}
	s.Sold -= units
	f.stock[variantID] = s
	f.movements[key] = true
	if listingID, ok := f.variantListing[variantID]; ok {
		if at := f.listingAt(listingID); at >= 0 {
			f.listings[at].listing.CachedSold = max(0, f.listings[at].listing.CachedSold-units)
		}
	}
	return nil
}

// SetCachedRating is trust handing over the review average, which cannot be joined across
// schemas. A listing that has gone is not an error: its reviews outlive it.
func (f *fakeRepo) SetCachedRating(_ context.Context, listingID int64, rating float64, count int64) error {
	if at := f.listingAt(listingID); at >= 0 {
		f.listings[at].listing.CachedRating = rating
		f.listings[at].listing.CachedReviewCount = count
	}
	return nil
}

// --- the listing aggregate ---

func (f *fakeRepo) listingAt(id int64) int {
	return slices.IndexFunc(f.listings, func(s storedListing) bool { return s.listing.ID == id })
}

func (f *fakeRepo) CreateListing(_ context.Context, l *domain.Listing, actor int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	// The slug is globally unique, as "listing_slug_key" is.
	if slices.ContainsFunc(f.listings, func(s storedListing) bool { return s.listing.Slug == l.Slug }) {
		return domain.ErrSlugTaken
	}
	if _, ok := f.categories[l.CategoryID]; !ok {
		return domain.ErrCategoryNotFound
	}
	l.ID = f.id()
	l.Version = 1
	l.CreatedAt = time.Now()
	if err := f.putListing(l); err != nil {
		return err
	}
	f.recordTrail(l, actor)
	l.ClearEvents()
	return nil
}

func (f *fakeRepo) SaveListing(_ context.Context, l *domain.Listing, actor int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	at := f.listingAt(l.ID)
	// A stale version loses, exactly as `WHERE version = @version` does.
	if at < 0 || f.listings[at].listing.Version != l.Version || f.listings[at].listing.DeletedAt != nil {
		return domain.ErrVersionConflict
	}
	if _, ok := f.categories[l.CategoryID]; !ok {
		return domain.ErrCategoryNotFound
	}
	l.Version++
	if err := f.putListing(l); err != nil {
		l.Version--
		return err
	}
	f.recordTrail(l, actor)
	l.ClearEvents()
	return nil
}

// recordTrail is the audit half of a write: one row per fact the root recorded, with the
// snapshot as it now is.
func (f *fakeRepo) recordTrail(l *domain.Listing, actor int64) {
	var changedBy *int64
	if actor != 0 {
		changedBy = &actor
	}
	snapshot := l.Snapshot()
	for _, e := range l.Events() {
		f.audit = append(f.audit, common.AuditEntry{
			Table: "listing", RecordID: l.ID, ChangeType: "update", Code: string(e.Code),
			ChangedBy: changedBy, Diff: e.Payload, Snapshot: snapshot,
		})
	}
}

// putListing writes the root and syncs the children the way the adapter does: a variant with
// no id is inserted with its stock row, one absent from the slice is soft deleted, and the
// tag slice is the whole set.
func (f *fakeRepo) putListing(l *domain.Listing) error {
	live := make(map[string]bool, len(l.Variants))
	featured := 0
	for _, v := range l.Variants {
		if !v.IsLive() {
			continue
		}
		// "variant_one_featured_per_listing": at most one live featured variant.
		if v.IsFeatured {
			featured++
			if featured > 1 {
				return domain.ErrTooManyFeatured
			}
		}
		// "variant_listing_id_attributes_key": two live variants cannot describe the same
		// combination.
		key := fmt.Sprint(v.Attributes)
		if live[key] {
			return domain.ErrDuplicateVariant
		}
		live[key] = true
		if v.ID == 0 {
			v.ID = f.id()
			v.ListingID = l.ID
			f.stock[v.ID] = v.Stock
			f.variantListing[v.ID] = l.ID
			continue
		}
		// A seller edit writes the quantity only; reserved and sold move by their own
		// guarded statements.
		s := f.stock[v.ID]
		if v.Stock.Quantity < s.Committed() {
			return domain.ErrQuantityBelowCommitted
		}
		s.Quantity = v.Stock.Quantity
		f.stock[v.ID] = s
	}
	for _, tag := range l.Tags {
		if _, ok := f.tags[tag]; !ok {
			return domain.ErrTagNotFound
		}
	}
	stored := storedListing{listing: *l, tags: slices.Clone(l.Tags)}
	for _, v := range l.Variants {
		// A soft-deleted variant leaves the join, as `v.deleted_at IS NULL` takes it out of the
		// adapter's lookup.
		if !v.IsLive() {
			delete(f.variantListing, v.ID)
		}
		stored.variants = append(stored.variants, *v)
	}
	if at := f.listingAt(l.ID); at >= 0 {
		f.listings[at] = stored
		return nil
	}
	f.listings = append(f.listings, stored)
	return nil
}

func (f *fakeRepo) GetListing(_ context.Context, id int64) (*domain.Listing, error) {
	at := f.listingAt(id)
	if at < 0 || f.listings[at].listing.DeletedAt != nil {
		return nil, domain.ErrListingNotFound
	}
	return f.hydrate(f.listings[at]), nil
}

func (f *fakeRepo) GetListingForSeller(ctx context.Context, id, sellerID int64) (*domain.Listing, error) {
	l, err := f.GetListing(ctx, id)
	if err != nil {
		return nil, err
	}
	// Ownership is part of the lookup, so another seller's listing is not found.
	if l.SellerID != sellerID {
		return nil, domain.ErrListingNotFound
	}
	return l, nil
}

// hydrate rebuilds the aggregate the way the loader does: live variants only, with their
// stock read from the stock table rather than from whatever the last Save happened to hold.
func (f *fakeRepo) hydrate(stored storedListing) *domain.Listing {
	l := stored.listing
	l.Tags = slices.Clone(stored.tags)
	l.Variants = nil
	for _, v := range stored.variants {
		if !v.IsLive() {
			continue
		}
		copied := v
		copied.Stock = f.stock[v.ID]
		l.Variants = append(l.Variants, &copied)
	}
	return &l
}

func (f *fakeRepo) IsFavorited(_ context.Context, accountID, listingID int64) (bool, error) {
	if accountID == 0 {
		return false, nil
	}
	return f.favorites[[2]int64{accountID, listingID}], nil
}

func (f *fakeRepo) CountFavorites(_ context.Context, listingID int64) (int64, error) {
	var n int64
	for pair := range f.favorites {
		if pair[1] == listingID {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) GetListingByVariant(ctx context.Context, variantID, sellerID int64) (*domain.Listing, error) {
	listingID, ok := f.variantListing[variantID]
	if !ok {
		return nil, domain.ErrVariantNotFound
	}
	l, err := f.GetListingForSeller(ctx, listingID, sellerID)
	if err != nil {
		// Another seller's variant is not found rather than forbidden.
		return nil, domain.ErrVariantNotFound
	}
	return l, nil
}

func (f *fakeRepo) SoftDeleteListing(_ context.Context, id, sellerID, actor int64) error {
	at := f.listingAt(id)
	if at < 0 || f.listings[at].listing.SellerID != sellerID || f.listings[at].listing.DeletedAt != nil {
		return domain.ErrListingNotFound
	}
	// The adapter carries this in its WHERE clause, so a reservation that landed after the
	// service read loses here rather than deleting a listing with units held.
	for _, v := range f.listings[at].variants {
		if v.DeletedAt == nil && f.stock[v.ID].Reserved > 0 {
			return domain.ErrListingInUse
		}
	}
	now := time.Now()
	f.listings[at].listing.DeletedAt = &now
	f.listings[at].listing.Version++
	var changedBy *int64
	if actor != 0 {
		changedBy = &actor
	}
	f.audit = append(f.audit, common.AuditEntry{
		Table: "listing", RecordID: id, ChangeType: "delete",
		Code: string(domain.Deleted.Code), ChangedBy: changedBy,
		Diff: domain.NoPayload{}, Snapshot: domain.NoPayload{},
	})
	return nil
}

func (f *fakeRepo) ListModerationQueue(_ context.Context, filter port.QueueFilter) ([]port.ListingSummary, int64, error) {
	var matched []port.ListingSummary
	for _, stored := range f.listings {
		l := stored.listing
		if l.DeletedAt != nil {
			continue
		}
		switch {
		case filter.Status != "":
			if l.Status != filter.Status {
				continue
			}
		default:
			// Both halves of the queue: awaiting a first publication, or holding an edit.
			if l.Status != domain.StatusPending && l.PendingEdit == nil {
				continue
			}
		}
		if filter.SellerID != 0 && l.SellerID != filter.SellerID {
			continue
		}
		// The featured variant's price, else the cheapest — what
		// `ORDER BY is_featured DESC, price LIMIT 1` answers.
		var price int64
		for _, v := range stored.variants {
			if v.DeletedAt != nil {
				continue
			}
			if v.IsFeatured {
				price = v.Price
				break
			}
			if price == 0 || v.Price < price {
				price = v.Price
			}
		}
		var coverID *int64
		if len(l.Attachments) > 0 {
			coverID = new(l.Attachments[0])
		}
		matched = append(matched, port.ListingSummary{
			ID: l.ID, SellerID: l.SellerID, Slug: l.Slug, Name: l.Name, Status: l.Status,
			Condition: l.Condition, PriceMode: l.PriceMode, Currency: l.Currency,
			Price: price, Sold: l.CachedSold, Rating: l.CachedRating,
			CategoryID: l.CategoryID, CoverID: coverID,
			CreatedAt: l.CreatedAt,
		})
	}
	slices.SortFunc(matched, func(a, b port.ListingSummary) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

// auditedDiff finds the payload recorded under one event type, at that type. A test that
// asserts on the trail should not be reaching into a map to do it.
func auditedDiff[T any](f *fakeRepo, e domain.EventType[T]) (T, bool) {
	for _, entry := range f.audit {
		if entry.Code != string(e.Code) {
			continue
		}
		if payload, ok := entry.Diff.(T); ok {
			return payload, true
		}
	}
	var zero T
	return zero, false
}

// --- the browse feed ---

// ListListings applies the same filters and visibility rules the statement does. It is longer
// than the others on purpose: this is the one read whose rules a service test can get wrong
// without a database noticing.
func (f *fakeRepo) ListListings(_ context.Context, filter port.ListingFilter) ([]port.ListingSummary, int64, error) {
	var matched []port.ListingSummary
	for _, stored := range f.listings {
		l := stored.listing
		if len(filter.VariantIDs) > 0 {
			// Resolving a variant to its listing, with the same visibility rule an id
			// lookup has.
			var holds bool
			for _, v := range stored.variants {
				if slices.Contains(filter.VariantIDs, v.ID) {
					holds = true
				}
			}
			if !holds {
				continue
			}
			if (l.Status == domain.StatusDraft || l.Status == domain.StatusPending) && l.SellerID != filter.ViewerID {
				continue
			}
		} else if len(filter.IDs) > 0 {
			// An id lookup ignores every other filter and answers for hidden and deleted rows;
			// only "never public" stays out unless the caller owns it.
			if !slices.Contains(filter.IDs, l.ID) {
				continue
			}
			if (l.Status == domain.StatusDraft || l.Status == domain.StatusPending) && l.SellerID != filter.ViewerID {
				continue
			}
		} else {
			if l.DeletedAt != nil {
				continue
			}
			if filter.Mine {
				if l.SellerID != filter.ViewerID {
					continue
				}
				if filter.Status != "" && l.Status != filter.Status {
					continue
				}
			} else if l.Status != domain.StatusActive {
				continue
			}
			if filter.Favorited && !f.favorites[[2]int64{filter.ViewerID, l.ID}] {
				continue
			}
			if filter.CategoryID != 0 && l.CategoryID != filter.CategoryID {
				continue
			}
			if filter.SellerID != 0 && l.SellerID != filter.SellerID {
				continue
			}
			if filter.Condition != "" && l.Condition != filter.Condition {
				continue
			}
			if filter.Tag != "" && !slices.Contains(stored.tags, filter.Tag) {
				continue
			}
			if filter.Query != "" && !strings.Contains(strings.ToLower(l.Name), strings.ToLower(filter.Query)) {
				continue
			}
			if !priceInRange(stored, filter) {
				continue
			}
			if !inArea(l.Location, filter) {
				continue
			}
			if !withinRadius(l.Location, filter) {
				continue
			}
		}
		card := f.summaryOf(stored)
		// Every card carries its distance once the browse said where the buyer is, which is what
		// lets a client render "2 km away" without asking for it.
		if filter.Near != nil && l.Location != nil && l.Location.Geocoded() {
			card.DistanceKM = new(distanceKM(*filter.Near, *l.Location))
		}
		matched = append(matched, card)
	}
	sortFeed(matched, filter.Sort)
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

// priceInRange is satisfied by any one live variant, as the EXISTS subqueries are.
// MaxPrice is a pointer: nil is "not filtered", but a set 0 is a legal bound that no
// price (gte=1) can ever satisfy — unlike MinPrice, where 0 really is a no-op.
func priceInRange(stored storedListing, filter port.ListingFilter) bool {
	if filter.MinPrice == 0 && filter.MaxPrice == nil {
		return true
	}
	okMin, okMax := filter.MinPrice == 0, filter.MaxPrice == nil
	for _, v := range stored.variants {
		if v.DeletedAt != nil {
			continue
		}
		if filter.MinPrice != 0 && v.Price >= filter.MinPrice {
			okMin = true
		}
		if filter.MaxPrice != nil && v.Price <= *filter.MaxPrice {
			okMax = true
		}
	}
	return okMin && okMax
}

// inArea matches the administrative filter against the listing's snapshot, as the SQL does. A
// listing with no location is out of any area filter: it was never published.
func inArea(area *domain.Location, filter port.ListingFilter) bool {
	if filter.ProvinceCode == "" && filter.DistrictCode == "" && filter.WardCode == "" {
		return true
	}
	if area == nil {
		return false
	}
	if filter.ProvinceCode != "" && area.ProvinceCode != filter.ProvinceCode {
		return false
	}
	if filter.DistrictCode != "" && (area.DistrictCode == nil || *area.DistrictCode != filter.DistrictCode) {
		return false
	}
	return filter.WardCode == "" || area.WardCode == filter.WardCode
}

// withinRadius is ST_DWithin's answer. A listing with no point is outside every circle rather than
// at its centre: it cannot claim a distance it has no way to know.
func withinRadius(area *domain.Location, filter port.ListingFilter) bool {
	if filter.Near == nil || filter.RadiusKM <= 0 {
		return true
	}
	if area == nil || !area.Geocoded() {
		return false
	}
	return distanceKM(*filter.Near, *area) <= filter.RadiusKM
}

// distanceKM is the great-circle distance, which is what ST_Distance on a geography answers to
// within the precision any of these tests cares about.
func distanceKM(from port.Point, area domain.Location) float64 {
	const earthKM = 6371.0
	lat1, lon1 := from.Latitude*math.Pi/180, from.Longitude*math.Pi/180
	lat2, lon2 := *area.Latitude*math.Pi/180, *area.Longitude*math.Pi/180
	h := math.Sin((lat2-lat1)/2)*math.Sin((lat2-lat1)/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin((lon2-lon1)/2)*math.Sin((lon2-lon1)/2)
	return 2 * earthKM * math.Asin(math.Sqrt(h))
}

func (f *fakeRepo) summaryOf(stored storedListing) port.ListingSummary {
	l := stored.listing
	// The cheapest live variant, as the lateral join reads it.
	var price int64
	for _, v := range stored.variants {
		if v.DeletedAt == nil && (price == 0 || v.Price < price) {
			price = v.Price
		}
	}
	var coverID *int64
	if len(l.Attachments) > 0 {
		coverID = new(l.Attachments[0])
	}
	return port.ListingSummary{
		ID: l.ID, SellerID: l.SellerID, Slug: l.Slug, Name: l.Name, Status: l.Status,
		Condition: l.Condition, PriceMode: l.PriceMode, Currency: l.Currency, Price: price,
		Sold: l.CachedSold, Rating: l.CachedRating, CategoryID: l.CategoryID, CoverID: coverID,
		Location: l.Location, CreatedAt: l.CreatedAt, DeletedAt: l.DeletedAt,
	}
}

func sortFeed(rows []port.ListingSummary, order string) {
	switch order {
	case port.SortDistance:
		// Nearest first, and an unknown distance last rather than first.
		slices.SortFunc(rows, func(a, b port.ListingSummary) int {
			switch {
			case a.DistanceKM == nil && b.DistanceKM == nil:
				return 0
			case a.DistanceKM == nil:
				return 1
			case b.DistanceKM == nil:
				return -1
			}
			return cmp.Compare(*a.DistanceKM, *b.DistanceKM)
		})
	case port.SortRating:
		slices.SortFunc(rows, func(a, b port.ListingSummary) int { return cmp.Compare(b.Rating, a.Rating) })
	case port.SortPriceAsc:
		slices.SortFunc(rows, func(a, b port.ListingSummary) int { return cmp.Compare(a.Price, b.Price) })
	case port.SortPriceDesc:
		slices.SortFunc(rows, func(a, b port.ListingSummary) int { return cmp.Compare(b.Price, a.Price) })
	case port.SortBestSelling:
		slices.SortFunc(rows, func(a, b port.ListingSummary) int { return cmp.Compare(b.Sold, a.Sold) })
	default:
		slices.SortFunc(rows, func(a, b port.ListingSummary) int { return cmp.Compare(b.ID, a.ID) })
	}
}

func (f *fakeRepo) InterestVectors(_ context.Context, accountID int64) ([]port.Vector, error) {
	return f.interests[accountID], nil
}

func (f *fakeRepo) FavoritedAmong(_ context.Context, accountID int64, listingIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(listingIDs))
	for _, listingID := range listingIDs {
		if f.favorites[[2]int64{accountID, listingID}] {
			out[listingID] = true
		}
	}
	return out, nil
}

func (f *fakeRepo) AddFavorite(_ context.Context, accountID, listingID int64) error {
	if f.listingAt(listingID) < 0 {
		return domain.ErrListingNotFound
	}
	f.favorites[[2]int64{accountID, listingID}] = true
	return nil
}

func (f *fakeRepo) RemoveFavorite(_ context.Context, accountID, listingID int64) error {
	delete(f.favorites, [2]int64{accountID, listingID})
	return nil
}

// fakeUploads is the upload seam a service test needs: it records a slot per resource id and
// resolves a confirmed one, refusing what the real store refuses — an unconfirmed id, another
// uploader's slot, and bytes that never arrived.
type fakeUploads struct {
	nextID int64
	// slots is what Presign handed out, pending is whether it has been confirmed, and owner is
	// who may confirm it.
	slots     map[int64]bool
	owner     map[int64]int64
	confirmed map[int64]bool
	// arrived is whether the client actually uploaded. A confirm without it is refused, which
	// is what stops a row rendering as a broken image.
	arrived map[int64]bool
	// videos are the resources stored as a clip rather than a picture, so a test can hand the
	// suggestion route an unboxing video the way a seller now can.
	videos map[int64]bool
}

func newFakeUploads() *fakeUploads {
	return &fakeUploads{
		slots: map[int64]bool{}, owner: map[int64]int64{},
		confirmed: map[int64]bool{}, arrived: map[int64]bool{}, videos: map[int64]bool{},
	}
}

func (f *fakeUploads) Presign(_ context.Context, uploaderID int64, _ string, req common.UploadRequest) (common.UploadSlot, error) {
	f.nextID++
	f.slots[f.nextID] = true
	f.owner[f.nextID] = uploaderID
	return common.UploadSlot{
		ResourceID: f.nextID,
		URL:        "https://store.test/put/" + strconv.FormatInt(f.nextID, 10),
		ExpiresAt:  time.Now().Add(15 * time.Minute),
	}, nil
}

func (f *fakeUploads) Confirm(_ context.Context, uploaderID, resourceID int64) (common.Resource, error) {
	if !f.slots[resourceID] || f.confirmed[resourceID] || f.owner[resourceID] != uploaderID {
		return common.Resource{}, common.ErrResourceNotFound
	}
	if !f.arrived[resourceID] {
		return common.Resource{}, storage.ErrObjectNotFound
	}
	f.confirmed[resourceID] = true
	return common.Resource{ID: resourceID, Provider: "test", ObjectKey: "k", Mime: "image/jpeg"}, nil
}

func (f *fakeUploads) Resolve(_ context.Context, ids []int64) (map[int64]common.ResourceDTO, error) {
	out := make(map[int64]common.ResourceDTO, len(ids))
	for _, one := range ids {
		if !f.confirmed[one] {
			continue
		}
		out[one] = common.Resource{
			ID: one, Provider: "test", ObjectKey: "k", Mime: f.mimeOf(one),
			URL: "https://store.test/get/" + strconv.FormatInt(one, 10),
		}.ToDTO()
	}
	return out, nil
}

// Bytes is what the suggestion route reads a photo through: only a confirmed upload has any, and the
// content is a stand-in — what a test checks is which ids reached the model, not the pixels.
// mimeOf is what the row says it holds, which is the only thing the suggestion route reads before
// deciding whether to pull the bytes at all.
func (f *fakeUploads) mimeOf(id int64) string {
	if f.videos[id] {
		return "video/mp4"
	}
	return "image/jpeg"
}

func (f *fakeUploads) Bytes(_ context.Context, ids []int64) ([]common.Blob, error) {
	out := make([]common.Blob, 0, len(ids))
	for _, id := range ids {
		if !f.confirmed[id] {
			continue
		}
		out = append(out, common.Blob{
			ResourceID: id, Mime: f.mimeOf(id),
			Data: []byte("photo-" + strconv.FormatInt(id, 10)),
		})
	}
	return out, nil
}

package domain_test

import (
	"errors"
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

func listingInput() domain.NewListingInput {
	return domain.NewListingInput{
		Name:           "Áo thun Uniqlo",
		Description:    "Còn mới",
		Condition:      domain.ConditionUsed,
		PriceMode:      domain.PriceModeFixed,
		Currency:       "VND",
		Specifications: map[string]any{"brand": "Uniqlo"},
		Tags:           []string{"handmade"},
		Variants:       []domain.NewVariantInput{variantInput()},
	}
}

func newListing(t *testing.T) *domain.Listing {
	t.Helper()
	l, err := domain.NewListing(7, 3, listingInput())
	if err != nil {
		t.Fatalf("NewListing: %v", err)
	}
	// The service attaches this from the seller's pickup address before it publishes. Set here so
	// every lifecycle test starts from a listing that *can* go live; the rule itself has its own
	// test below.
	l.Location = sellerLocation()
	return l
}

// sellerLocation is where the goods are: Ben Nghe ward, District 1, Ho Chi Minh City.
func sellerLocation() *domain.Location {
	return &domain.Location{
		ProvinceCode: "79", ProvinceName: "Ho Chi Minh",
		DistrictCode: new("760"), DistrictName: new("District 1"),
		WardCode: "26734", WardName: "Ben Nghe",
		Latitude: new(10.7769), Longitude: new(106.7009),
	}
}

// A listing goes live with an address or not at all: it is where a carrier collects, so a live
// listing without one is browsable and impossible to buy — which is where it used to surface, at
// the buyer's checkout.
func TestPublish_NeedsAPickupAddress(t *testing.T) {
	l := newListing(t)
	l.Location = nil
	if err := l.Publish(); !errors.Is(err, domain.ErrNoPickupAddress) {
		t.Fatalf("Publish with no address = %v, want ErrNoPickupAddress", err)
	}
	if l.Status != domain.StatusDraft {
		t.Fatalf("status = %q, want it left in draft", l.Status)
	}
	l.Location = sellerLocation()
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// A listing is born in draft with at least one variant: the create request carries them
// inline, so an empty listing is never a state that exists.
func TestNewListing(t *testing.T) {
	l := newListing(t)
	if l.Status != domain.StatusDraft {
		t.Errorf("status = %q, want draft", l.Status)
	}
	if l.Version != 1 {
		t.Errorf("version = %d, want 1", l.Version)
	}
	if len(l.Variants) != 1 {
		t.Fatalf("variants = %d, want 1", len(l.Variants))
	}
	// The first variant is the featured one until the seller says otherwise: a card has to
	// show something.
	if !l.Variants[0].IsFeatured {
		t.Error("the only variant is not featured")
	}
	if l.Slug == "" {
		t.Error("slug was not derived from the name")
	}
}

func TestNewListing_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*domain.NewListingInput)
		want  error
	}{
		{name: "no variant", build: func(in *domain.NewListingInput) { in.Variants = nil }, want: domain.ErrNoVariant},
		{name: "eleven tags", build: func(in *domain.NewListingInput) {
			in.Tags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
		}, want: domain.ErrTooManyTags},
		{name: "duplicate variants", build: func(in *domain.NewListingInput) {
			in.Variants = []domain.NewVariantInput{variantInput(), variantInput()}
		}, want: domain.ErrDuplicateVariant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := listingInput()
			tc.build(&in)
			if _, err := domain.NewListing(7, 3, in); !errors.Is(err, tc.want) {
				t.Fatalf("NewListing = %v, want %v", err, tc.want)
			}
		})
	}
}

// A duplicate tag is not an error, it is the same tag twice — deduplicated, because the
// join has a unique key and a client sending it twice meant it once.
func TestNewListing_DeduplicatesTags(t *testing.T) {
	in := listingInput()
	in.Tags = []string{"handmade", "handmade", "eco-friendly"}
	l, err := domain.NewListing(7, 3, in)
	if err != nil {
		t.Fatalf("NewListing: %v", err)
	}
	if len(l.Tags) != 2 {
		t.Fatalf("tags = %v, want two", l.Tags)
	}
}

// Publication always enters moderation. There is no path from draft to active, which is
// what stops a seller from re-publishing something a moderator took down.
func TestPublish_AlwaysEntersModeration(t *testing.T) {
	l := newListing(t)
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if l.Status != domain.StatusPending {
		t.Fatalf("status = %q, want pending", l.Status)
	}
	if !l.Happened(domain.Published.Code) {
		t.Error("the submission was not recorded")
	}
	// Twice is a conflict: there is already something in the queue.
	if err := l.Publish(); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("second Publish = %v, want ErrInvalidTransition", err)
	}
}

func TestPublish_NeedsALiveVariant(t *testing.T) {
	l := newListing(t)
	if err := l.RemoveVariant(l.Variants[0].ID); err != nil {
		t.Fatalf("RemoveVariant: %v", err)
	}
	if err := l.Publish(); !errors.Is(err, domain.ErrNoVariant) {
		t.Fatalf("Publish = %v, want ErrNoVariant", err)
	}
}

// The lifecycle in one test: approve makes it live, a takedown hides it with a reason, and
// publishing again goes back through the queue rather than straight to active.
func TestLifecycle(t *testing.T) {
	l := newListing(t)
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if l.Status != domain.StatusActive {
		t.Fatalf("status = %q, want active", l.Status)
	}
	if err := l.Takedown("counterfeit", true); err != nil {
		t.Fatalf("Takedown: %v", err)
	}
	if l.Status != domain.StatusHidden {
		t.Fatalf("status = %q, want hidden", l.Status)
	}
	if err := l.Publish(); err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if l.Status != domain.StatusPending {
		t.Fatalf("status = %q, want pending — a takedown cannot be undone by the seller", l.Status)
	}
}

func TestApprove_NothingToApprove(t *testing.T) {
	l := newListing(t)
	if err := l.Approve(""); !errors.Is(err, domain.ErrNotAwaitingModeration) {
		t.Fatalf("Approve on a draft = %v, want ErrNotAwaitingModeration", err)
	}
}

// Editing a live listing parks the change instead of unpublishing it: buyers keep seeing
// the approved version until a moderator applies the edit.
func TestSubmitEdit_KeepsTheListingLive(t *testing.T) {
	l := newListing(t)
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	name := "Áo thun Uniqlo cổ tròn"
	if err := l.SubmitEdit(domain.PendingEdit{Name: &name}); err != nil {
		t.Fatalf("SubmitEdit: %v", err)
	}
	if l.Status != domain.StatusActive {
		t.Errorf("status = %q, want it still active", l.Status)
	}
	if l.Name == name {
		t.Error("the live row was rewritten; the edit must be held")
	}
	if l.PendingEdit == nil || l.PendingEdit.Name == nil {
		t.Fatal("the edit was not held")
	}
	if err := l.ApplyPendingEdit(); err != nil {
		t.Fatalf("ApplyPendingEdit: %v", err)
	}
	if l.Name != name || l.PendingEdit != nil {
		t.Fatalf("listing = %+v, want the edit applied and cleared", l)
	}
}

// A draft is edited in place: there is nothing published to protect.
func TestSubmitEdit_DraftIsWrittenDirectly(t *testing.T) {
	l := newListing(t)
	name := "Renamed"
	if err := l.SubmitEdit(domain.PendingEdit{Name: &name}); err != nil {
		t.Fatalf("SubmitEdit: %v", err)
	}
	if l.Name != name || l.PendingEdit != nil {
		t.Fatalf("listing = %+v, want the draft written directly", l)
	}
}

// The featured variant is chosen from the listing's own set, and the last live variant of a
// live listing cannot be removed.
func TestVariants(t *testing.T) {
	l := newListing(t)
	first := l.Variants[0]
	second, err := domain.NewVariant(domain.NewVariantInput{
		Price: 199000, Attributes: map[string]any{"size": "m"},
		PackageDetails: map[string]any{}, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("NewVariant: %v", err)
	}
	if err := l.AddVariant(second); err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	// Ids are the database's, filled in by Save; a test that addresses a variant by id has
	// to stand in for it, or every unsaved variant answers to 0.
	first.ID, second.ID = 1, 2
	if err := l.SetFeatured(second.ID); err != nil {
		t.Fatalf("SetFeatured: %v", err)
	}
	if first.IsFeatured || !second.IsFeatured {
		t.Error("the featured flag did not move")
	}
	if err := l.SetFeatured(999); !errors.Is(err, domain.ErrVariantNotFound) {
		t.Errorf("SetFeatured on a stranger = %v, want ErrVariantNotFound", err)
	}

	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := l.RemoveVariant(first.ID); err != nil {
		t.Fatalf("RemoveVariant: %v", err)
	}
	if err := l.RemoveVariant(second.ID); !errors.Is(err, domain.ErrLastVariant) {
		t.Fatalf("RemoveVariant = %v, want ErrLastVariant", err)
	}
}

// Removing the featured variant moves the flag rather than leaving the card with nothing.
func TestRemoveVariant_MovesTheFeaturedFlag(t *testing.T) {
	l := newListing(t)
	second, _ := domain.NewVariant(domain.NewVariantInput{
		Price: 199000, Attributes: map[string]any{"size": "m"},
		PackageDetails: map[string]any{}, Quantity: 2,
	})
	if err := l.AddVariant(second); err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	l.Variants[0].ID, second.ID = 1, 2
	if err := l.RemoveVariant(l.Variants[0].ID); err != nil {
		t.Fatalf("RemoveVariant: %v", err)
	}
	if !second.IsFeatured {
		t.Error("the flag did not move to the surviving variant")
	}
}

// A listing still in the queue has no approved version to protect, so an edit is written
// through rather than held — otherwise approving it would flip the status and silently leave
// the reviewed change unapplied, putting the listing straight back in the queue.
func TestSubmitEdit_PendingWritesThrough(t *testing.T) {
	l := newListing(t)
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	name := "Renamed while queued"
	if err := l.SubmitEdit(domain.PendingEdit{Name: &name}); err != nil {
		t.Fatalf("SubmitEdit: %v", err)
	}
	if l.Name != name || l.PendingEdit != nil {
		t.Fatalf("listing = %+v, want the edit written through", l)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if l.Status != domain.StatusActive || l.PendingEdit != nil {
		t.Fatalf("listing = %+v, want it live with nothing left to review", l)
	}
}

// A draft may be emptied: DELETE /variants only refuses the last variant of a listing that is
// live or queued, so Publish is where "there has to be something to buy" is checked.
func TestValidate_AnEmptyDraftIsStorable(t *testing.T) {
	l := newListing(t)
	if err := l.RemoveVariant(l.Variants[0].ID); err != nil {
		t.Fatalf("RemoveVariant: %v", err)
	}
	if err := l.Validate(); err != nil {
		t.Fatalf("Validate on an emptied draft: %v", err)
	}
	if err := l.Publish(); !errors.Is(err, domain.ErrNoVariant) {
		t.Fatalf("Publish = %v, want ErrNoVariant", err)
	}
	// A create still has to carry one: the request brings them inline.
	in := listingInput()
	in.Variants = nil
	if _, err := domain.NewListing(7, 3, in); !errors.Is(err, domain.ErrNoVariant) {
		t.Fatalf("NewListing = %v, want ErrNoVariant", err)
	}
}

// A name of only punctuation derives an empty slug, and the link that listing publishes would
// then be a bare id — so the name is refused rather than the link being unreadable.
func TestNewListing_NameMustDeriveASlug(t *testing.T) {
	in := listingInput()
	in.Name = "!!! ???"
	if _, err := domain.NewListing(7, 3, in); err == nil {
		t.Fatal("a name with no letter or digit was accepted")
	}
}

// Two live variants both claiming the card is its own error: "duplicate_variant" used to
// answer this, which told the caller about attributes it had not touched.
func TestValidate_OnlyOneFeatured(t *testing.T) {
	l := newListing(t)
	second, err := domain.NewVariant(domain.NewVariantInput{
		Price: 1000, Attributes: map[string]any{"size": "m"},
		PackageDetails: map[string]any{}, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("NewVariant: %v", err)
	}
	if err := l.AddVariant(second); err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	second.IsFeatured = true
	if err := l.Validate(); !errors.Is(err, domain.ErrTooManyFeatured) {
		t.Fatalf("Validate = %v, want ErrTooManyFeatured", err)
	}
}

// The attribute key keeps the value's type, so {"size": 1} and {"size": "1"} stay the two
// distinct rows "variant_listing_id_attributes_key" allows.
func TestValidate_AttributeTypesAreDistinct(t *testing.T) {
	l := newListing(t)
	l.Variants[0].Attributes = map[string]any{"size": 1}
	second, err := domain.NewVariant(domain.NewVariantInput{
		Price: 1000, Attributes: map[string]any{"size": "1"},
		PackageDetails: map[string]any{}, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("NewVariant: %v", err)
	}
	if err := l.AddVariant(second); err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	if err := l.Validate(); err != nil {
		t.Fatalf("Validate: %v — a number and a string are two jsonb values", err)
	}
}

// The vector is built from the name, the category, the tags, the specification values and the
// description — and from nothing else. An edit that only reorders the photos, or switches the
// condition, changes no word the model reads, so marking it stale buys a transformer pass that
// produces the vector the row already has. At a million listings that is the difference between
// a queue that keeps up and one that never empties.
func TestApplyEdit_OnlyTheEmbeddedFieldsMakeTheVectorStale(t *testing.T) {
	fresh := func(t *testing.T) *domain.Listing {
		t.Helper()
		l := newListing(t)
		// NewListing marks a listing that has never been embedded. Clear it, so what the test
		// reads afterwards is what *this edit* decided.
		l.EmbeddingStaleAt = nil
		return l
	}

	unchanged := map[string]domain.PendingEdit{
		"photos":          {Attachments: []int64{9, 8, 7}},
		"condition":       {Condition: new(domain.ConditionNew)},
		"price mode":      {PriceMode: new(domain.PriceModeNegotiable)},
		"the same name":   {Name: new("Áo thun Uniqlo")},
		"the name padded": {Name: new("  Áo thun Uniqlo  ")},
		"the same tags":   {Tags: []string{"handmade"}},
		"tags reordered":  {Tags: []string{"handmade", "cotton"}},
		"the same specs":  {Specifications: map[string]any{"brand": "Uniqlo"}},
	}
	for name, edit := range unchanged {
		t.Run("stays fresh: "+name, func(t *testing.T) {
			l := fresh(t)
			if name == "tags reordered" {
				// Reordering only says nothing because the indexer sorts: the listing text is
				// built with string_agg(tag ORDER BY tag).
				l.Tags = []string{"cotton", "handmade"}
			}
			if err := l.SubmitEdit(edit); err != nil {
				t.Fatalf("SubmitEdit: %v", err)
			}
			if l.EmbeddingStaleAt != nil {
				t.Errorf("editing %s queued the listing for re-embedding", name)
			}
		})
	}

	changed := map[string]domain.PendingEdit{
		"name":           {Name: new("Áo thun Uniqlo cổ tròn")},
		"description":    {Description: new("Đã giặt một lần")},
		"category":       {CategoryID: new(int64(11))},
		"tags":           {Tags: []string{"handmade", "cotton"}},
		"specifications": {Specifications: map[string]any{"brand": "Uniqlo", "size": "L"}},
	}
	for name, edit := range changed {
		t.Run("goes stale: "+name, func(t *testing.T) {
			l := fresh(t)
			if err := l.SubmitEdit(edit); err != nil {
				t.Fatalf("SubmitEdit: %v", err)
			}
			if l.EmbeddingStaleAt == nil {
				t.Errorf("editing the %s left the old vector in place", name)
			}
		})
	}
}

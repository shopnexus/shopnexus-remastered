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
		ShippingPaidBy: domain.ShippingPaidByBuyer,
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
	return l
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
	if err := l.Approve(); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if l.Status != domain.StatusActive {
		t.Fatalf("status = %q, want active", l.Status)
	}
	if err := l.Takedown("counterfeit"); err != nil {
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
	if err := l.Approve(); !errors.Is(err, domain.ErrNotAwaitingModeration) {
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
	if err := l.Approve(); err != nil {
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
	if err := l.Approve(); err != nil {
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

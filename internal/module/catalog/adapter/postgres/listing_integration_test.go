//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/domain"
)

func newListingFor(t *testing.T, repo *pgadapter.Repo, categoryID int64, name string) *domain.Listing {
	t.Helper()
	l, err := domain.NewListing(7, categoryID, domain.NewListingInput{
		Name:           name,
		Description:    "probe",
		Condition:      domain.ConditionUsed,
		PriceMode:      domain.PriceModeFixed,
		ShippingPaidBy: domain.ShippingPaidByBuyer,
		Currency:       "VND",
		Tags:           []string{"handmade"},
		Variants: []domain.NewVariantInput{{
			Price: 299000, Attributes: map[string]any{"size": "l"},
			PackageDetails: map[string]any{}, Quantity: 5,
		}},
	})
	if err != nil {
		t.Fatalf("NewListing: %v", err)
	}
	if err := repo.CreateListing(context.Background(), l, 7); err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	// The category cleanup is RESTRICTed by this row, so it goes first (cleanups run LIFO).
	t.Cleanup(func() {
		if _, err := poolOf(t).Exec(context.Background(),
			`DELETE FROM listing WHERE id = @id`, pgx.NamedArgs{"id": l.ID}); err != nil {
			t.Logf("cleanup listing %d: %v", l.ID, err)
		}
	})
	return l
}

// Create writes the whole aggregate, and Get reads back exactly what was written —
// including the stock row a variant is born with.
func TestRepo_CreateAndGetListing(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	created := newListingFor(t, repo, category.ID, unique("Listing "))

	got, err := repo.GetListing(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if got.Version != 1 || got.Status != domain.StatusDraft {
		t.Fatalf("listing = %+v", got)
	}
	if len(got.Variants) != 1 || got.Variants[0].Stock.Quantity != 5 {
		t.Fatalf("variants = %+v", got.Variants)
	}
	if !got.Variants[0].IsFeatured {
		t.Error("the featured flag did not survive the round trip")
	}
	if len(got.Tags) != 1 || got.Tags[0] != "handmade" {
		t.Fatalf("tags = %v", got.Tags)
	}
	// Ownership is part of the lookup, so another seller's listing is not found at all.
	if _, err := repo.GetListingForSeller(ctx, created.ID, 8); !errors.Is(err, domain.ErrListingNotFound) {
		t.Errorf("GetListingForSeller for a stranger = %v, want ErrListingNotFound", err)
	}
}

// Two commands built on the same read: the second loses on the version check rather than
// overwriting what the first decided.
func TestRepo_SaveListingRefusesAStaleAggregate(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	created := newListingFor(t, repo, category.ID, unique("Listing "))

	first, err := repo.GetListing(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	stale, err := repo.GetListing(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if err := first.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := repo.SaveListing(ctx, first, 7); err != nil {
		t.Fatalf("first SaveListing: %v", err)
	}
	if err := stale.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := repo.SaveListing(ctx, stale, 7); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale SaveListing = %v, want ErrVersionConflict", err)
	}
}

// Save synchronises the children: a variant absent from the slice is soft deleted, a new one
// gets its stock row, and the tag join matches the slice.
func TestRepo_SaveListingSyncsChildren(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	for _, slug := range []string{"handmade", "eco-friendly"} {
		createTag(t, repo, slug, nil)
	}
	l := newListingFor(t, repo, category.ID, unique("Listing "))

	second, err := domain.NewVariant(domain.NewVariantInput{
		Price: 199000, Attributes: map[string]any{"size": "m"},
		PackageDetails: map[string]any{}, Quantity: 3,
	})
	if err != nil {
		t.Fatalf("NewVariant: %v", err)
	}
	if err := l.AddVariant(second); err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	l.Tags = []string{"eco-friendly"}
	if err := repo.SaveListing(ctx, l, 7); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	got, err := repo.GetListing(ctx, l.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if len(got.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(got.Variants))
	}
	if len(got.Tags) != 1 || got.Tags[0] != "eco-friendly" {
		t.Fatalf("tags = %v, want the slice to be the truth", got.Tags)
	}

	// Now remove the first: it comes back soft deleted, not gone, and Get leaves it out.
	if err := got.RemoveVariant(got.Variants[0].ID); err != nil {
		t.Fatalf("RemoveVariant: %v", err)
	}
	if err := repo.SaveListing(ctx, got, 7); err != nil {
		t.Fatalf("SaveListing (remove): %v", err)
	}
	after, err := repo.GetListing(ctx, l.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if len(after.Variants) != 1 {
		t.Fatalf("variants = %d, want the deleted one left out", len(after.Variants))
	}
	var deleted int
	if err := poolOf(t).QueryRow(ctx,
		`SELECT count(*) FROM variant WHERE listing_id = $1 AND deleted_at IS NOT NULL`,
		l.ID).Scan(&deleted); err != nil {
		t.Fatalf("count deleted: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("soft-deleted rows = %d, want 1 — order history has to stay resolvable", deleted)
	}
}

// The two structural rules the database owns, seen from Go: two live variants cannot carry
// the same attributes, and the slug is globally unique.
func TestRepo_SaveListingRefusesDuplicatesAndSlugs(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	name := unique("Listing ")
	first := newListingFor(t, repo, category.ID, name)

	taken, err := repo.SlugTaken(ctx, first.Slug)
	if err != nil || !taken {
		t.Fatalf("SlugTaken = %v, %v; want true", taken, err)
	}

	// The same derived slug twice.
	again, err := domain.NewListing(7, category.ID, domain.NewListingInput{
		Name: name, Description: "x", Condition: domain.ConditionUsed,
		PriceMode: domain.PriceModeFixed, ShippingPaidBy: domain.ShippingPaidByBuyer,
		Currency: "VND",
		Variants: []domain.NewVariantInput{{
			Price: 1000, Attributes: map[string]any{"size": "s"},
			PackageDetails: map[string]any{}, Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatalf("NewListing: %v", err)
	}
	if err := repo.CreateListing(ctx, again, 7); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("CreateListing = %v, want ErrSlugTaken", err)
	}

	// A second variant with the first's attributes, written past the domain check.
	dup, _ := domain.NewVariant(domain.NewVariantInput{
		Price: 1000, Attributes: map[string]any{"size": "l"},
		PackageDetails: map[string]any{}, Quantity: 1,
	})
	first.Variants = append(first.Variants, dup)
	if err := repo.SaveListing(ctx, first, 7); !errors.Is(err, domain.ErrDuplicateVariant) {
		t.Fatalf("SaveListing = %v, want ErrDuplicateVariant", err)
	}
}

// Every event the root recorded lands in audit_log in the same transaction as the change.
func TestRepo_SaveListingWritesTheTrail(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	l := newListingFor(t, repo, category.ID, unique("Listing "))
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := repo.SaveListing(ctx, l, 7); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}
	var code string
	err := poolOf(t).QueryRow(ctx,
		`SELECT code FROM audit_log WHERE table_name = 'listing' AND record_id = $1 ORDER BY id DESC LIMIT 1`,
		l.ID).Scan(&code)
	if err != nil {
		t.Fatalf("read trail: %v", err)
	}
	if code != string(domain.Published.Code) {
		t.Fatalf("code = %q, want %q", code, domain.Published.Code)
	}
	if len(l.Events()) != 0 {
		t.Error("Save left the events on the aggregate")
	}
}

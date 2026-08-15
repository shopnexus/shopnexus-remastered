//go:build integration

package postgres_test

import (
	"context"
	"testing"
)

// A name is not a key. Deleting a listing and posting the same goods again is an ordinary
// thing for a seller to do — the first listing is soft-deleted, so its row (and its slug)
// stays behind to keep order history resolvable, and the second one has to be creatable
// anyway.
func TestRepo_CreateListingAfterDeletingOneOfTheSameName(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	name := unique("Listing ")
	first := newListingFor(t, repo, category.ID, name)
	if err := repo.SoftDeleteListing(ctx, first.ID, testSeller, testSeller); err != nil {
		t.Fatalf("SoftDeleteListing: %v", err)
	}

	// newListingFor fails the test on error, which is the assertion: before the slug
	// constraint was dropped this answered ErrSlugTaken.
	second := newListingFor(t, repo, category.ID, name)
	if second.ID == first.ID {
		t.Fatal("the second listing reused the first one's row")
	}
}

// Two sellers offering the same phone are two listings — the whole reason a listing is not
// an entry in a shared product master. A name-derived slug that is unique across the table
// made the second seller's listing impossible to create.
func TestRepo_TwoSellersMayListTheSameName(t *testing.T) {
	repo := newRepo(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	name := unique("Listing ")
	first := newListingFor(t, repo, category.ID, name)

	other := newListingForSeller(t, repo, testSeller+1, category.ID, name)
	if other.Slug != first.Slug {
		t.Fatalf("slug = %q and %q, want the same name to derive the same slug", other.Slug, first.Slug)
	}
}

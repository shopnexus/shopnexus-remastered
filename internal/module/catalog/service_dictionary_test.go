package catalog_test

import (
	"context"
	"log/slog"
	"testing"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	"shopnexus/internal/module/catalog"
	catalogapi "shopnexus/internal/module/catalog/api"
	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// fakeAccounts answers the one question this module asks of the account module: the
// caller's role. It embeds the published stub, so an unstubbed call answers 501 rather
// than a plausible zero value.
type fakeAccounts struct {
	accounttest.Stub
	role string
}

func (f fakeAccounts) GetMe(context.Context, accountapi.GetMeRequest) (accountapi.Me, error) {
	return accountapi.Me{Role: f.role}, nil
}

type fakeResources struct{ commonapi.Service }

type harness struct {
	svc  *catalog.Service
	repo *fakeRepo
}

func newHarness(role string) *harness {
	repo := newFakeRepo()
	svc := catalog.NewService(repo, fakeAccounts{role: role}, fakeResources{},
		validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo}
}

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

const actor = id.ID[id.Account](1)

// Writing the tree is admin-only, and the check is the service's because the role is a
// row in the account module's table.
func TestAdminCreateCategory_PlainUserRefused(t *testing.T) {
	h := newHarness("user")
	_, err := h.svc.AdminCreateCategory(context.Background(), catalogapi.CreateCategoryRequest{
		ActorID: actor, Name: "Tops", Description: "",
	})
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestAdminCreateCategory_ThenListed(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	created, err := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{
		ActorID: actor, Name: "Tops", Description: "Shirts and tees",
	})
	if err != nil {
		t.Fatalf("AdminCreateCategory: %v", err)
	}
	if created.ID == 0 || created.Name != "Tops" || created.ParentID != nil {
		t.Fatalf("created = %+v", created)
	}
	list, err := h.svc.ListCategories(ctx, catalogapi.ListCategoriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}

// The same name twice is a 409, because a category is picked by name in the listing form.
func TestAdminCreateCategory_DuplicateNameConflicts(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	req := catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Tops"}
	if _, err := h.svc.AdminCreateCategory(ctx, req); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if got := status(t, mustErr(h.svc.AdminCreateCategory(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// A patch leaves what it does not mention, and clear_parent_id is how a node becomes a
// root — there is no null on the wire.
func TestAdminUpdateCategory_PatchAndClearParent(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	root, err := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	child, err := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{
		ActorID: actor, Name: "Child", ParentID: &root.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	renamed := "Renamed"
	got, err := h.svc.AdminUpdateCategory(ctx, catalogapi.UpdateCategoryRequest{
		ActorID: actor, ID: child.ID, Name: &renamed,
	})
	if err != nil {
		t.Fatalf("AdminUpdateCategory: %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name = %q", got.Name)
	}
	if got.ParentID == nil || *got.ParentID != root.ID {
		t.Errorf("parent = %v, want it untouched", got.ParentID)
	}

	got, err = h.svc.AdminUpdateCategory(ctx, catalogapi.UpdateCategoryRequest{
		ActorID: actor, ID: child.ID, ClearParentID: true,
	})
	if err != nil {
		t.Fatalf("AdminUpdateCategory (clear): %v", err)
	}
	if got.ParentID != nil {
		t.Errorf("parent = %v, want it cleared", got.ParentID)
	}
}

// Patching parent_id to an id nobody has is a 404, the same as the schema's FK violation
// on a dangling parent — the fake has to enforce that itself, since it holds no real FK.
func TestAdminUpdateCategory_UnknownParentNotFound(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	root, err := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	unknown := id.Of[id.Category](999999)
	err = mustErr(h.svc.AdminUpdateCategory(ctx, catalogapi.UpdateCategoryRequest{
		ActorID: actor, ID: root.ID, ParentID: &unknown,
	}))
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// Moving a node under its own descendant is refused, and the answer is the domain's
// rather than a driver error.
func TestAdminUpdateCategory_CycleRefused(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	root, _ := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Root"})
	child, _ := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{
		ActorID: actor, Name: "Child", ParentID: &root.ID,
	})
	err := mustErr(h.svc.AdminUpdateCategory(ctx, catalogapi.UpdateCategoryRequest{
		ActorID: actor, ID: root.ID, ParentID: &child.ID,
	}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// Deleting a category that listings still reference is refused; deleting a parent
// promotes its children instead of taking them with it.
func TestAdminDeleteCategory(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	root, _ := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Root"})
	child, _ := h.svc.AdminCreateCategory(ctx, catalogapi.CreateCategoryRequest{
		ActorID: actor, Name: "Child", ParentID: &root.ID,
	})

	h.repo.inUse[child.ID.Int64()] = true
	if got := status(t, h.svc.AdminDeleteCategory(ctx, catalogapi.DeleteCategoryRequest{
		ActorID: actor, ID: child.ID,
	})); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}

	if err := h.svc.AdminDeleteCategory(ctx, catalogapi.DeleteCategoryRequest{
		ActorID: actor, ID: root.ID,
	}); err != nil {
		t.Fatalf("AdminDeleteCategory: %v", err)
	}
	list, _ := h.svc.ListCategories(ctx, catalogapi.ListCategoriesRequest{Limit: 10})
	if len(list) != 1 || list[0].ParentID != nil {
		t.Fatalf("list = %+v, want the child promoted to a root", list)
	}
}

func mustErr[T any](_ T, err error) error { return err }

// --- tags ---

func TestAdminPutTag_IsIdempotentAndAdminOnly(t *testing.T) {
	ctx := context.Background()
	if got := status(t, mustErr(newHarness("user").svc.AdminPutTag(ctx, catalogapi.PutTagRequest{
		ActorID: actor, Slug: "handmade",
	}))); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}

	h := newHarness("admin")
	desc := "Made by hand"
	first, err := h.svc.AdminPutTag(ctx, catalogapi.PutTagRequest{ActorID: actor, Slug: "handmade", Description: &desc})
	if err != nil {
		t.Fatalf("AdminPutTag: %v", err)
	}
	if first.Slug != "handmade" || first.Description == nil || *first.Description != desc {
		t.Fatalf("tag = %+v", first)
	}
	// The same slug again is the same row: PUT is idempotent, and a body-less one clears the
	// description rather than conflicting.
	again, err := h.svc.AdminPutTag(ctx, catalogapi.PutTagRequest{ActorID: actor, Slug: "handmade"})
	if err != nil {
		t.Fatalf("AdminPutTag (again): %v", err)
	}
	if again.Description != nil {
		t.Errorf("description = %v, want nil", again.Description)
	}
	page, err := h.svc.ListTags(ctx, catalogapi.ListTagsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(page.Data) != 1 || page.Meta.TotalCount == nil || *page.Meta.TotalCount != 1 {
		t.Fatalf("page = %+v", page)
	}
}

// A malformed slug is refused by the domain, not by the database.
func TestAdminPutTag_BadSlugRejected(t *testing.T) {
	h := newHarness("admin")
	err := mustErr(h.svc.AdminPutTag(context.Background(), catalogapi.PutTagRequest{
		ActorID: actor, Slug: "Not A Slug",
	}))
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// q filters by what the picker typed; combining it with near is refused rather than silently
// ranking a set the prefix already decided.
func TestListTags_QueryAndNearAreExclusive(t *testing.T) {
	h := newHarness("user")
	err := mustErr(h.svc.ListTags(context.Background(), catalogapi.ListTagsRequest{
		Query: "hand", Near: []string{"handmade"}, Page: 1, Limit: 20,
	}))
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// The prefix is what the picker types, and the page is what keeps a growing dictionary
// answerable.
func TestListTags_PrefixAndPage(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	for _, slug := range []string{"handmade", "hand-dyed", "eco-friendly"} {
		if _, err := h.svc.AdminPutTag(ctx, catalogapi.PutTagRequest{ActorID: actor, Slug: slug}); err != nil {
			t.Fatalf("AdminPutTag(%q): %v", slug, err)
		}
	}
	page, err := h.svc.ListTags(ctx, catalogapi.ListTagsRequest{Query: "hand", Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("page = %+v, want the two hand* tags", page.Data)
	}
	// The window count is the whole match, not the page, or a pager cannot draw itself.
	second, err := h.svc.ListTags(ctx, catalogapi.ListTagsRequest{Query: "hand", Page: 2, Limit: 1})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(second.Data) != 1 || second.Data[0].Slug != "handmade" {
		t.Fatalf("second page = %+v", second.Data)
	}
	if second.Meta.TotalCount == nil || *second.Meta.TotalCount != 2 {
		t.Fatalf("total = %v, want 2", second.Meta.TotalCount)
	}
}

func TestAdminDeleteTag(t *testing.T) {
	h := newHarness("admin")
	ctx := context.Background()
	if _, err := h.svc.AdminPutTag(ctx, catalogapi.PutTagRequest{ActorID: actor, Slug: "handmade"}); err != nil {
		t.Fatalf("AdminPutTag: %v", err)
	}
	if err := h.svc.AdminDeleteTag(ctx, catalogapi.DeleteTagRequest{ActorID: actor, Slug: "handmade"}); err != nil {
		t.Fatalf("AdminDeleteTag: %v", err)
	}
	if got := status(t, h.svc.AdminDeleteTag(ctx, catalogapi.DeleteTagRequest{
		ActorID: actor, Slug: "handmade",
	})); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

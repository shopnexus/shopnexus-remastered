package main

import (
	"go/format"
	"strings"
	"testing"
)

func categoryTableFixture() *Table {
	id := &Column{Name: "id", Type: "uuid", Nullable: false, PrimaryKey: true}
	name := &Column{Name: "name", Type: "text", Nullable: false}
	desc := &Column{Name: "description", Type: "text", Nullable: false}
	parent := &Column{Name: "parent_id", Type: "uuid", Nullable: true}
	return &Table{
		Schema:      "catalog",
		Name:        "category",
		Columns:     []*Column{id, name, desc, parent},
		PrimaryKeys: []*Column{id},
	}
}

func categoryModelFixture() *ModelStruct {
	f := func(goName, goType, db string) ModelField {
		return ModelField{GoName: goName, GoType: goType, DBTag: db}
	}
	ms := &ModelStruct{Name: "Category", ByDB: map[string]ModelField{}, Imports: map[string]string{"uuid": "github.com/google/uuid"}}
	for _, mf := range []ModelField{
		f("ID", "uuid.UUID", "id"),
		f("Name", "string", "name"),
		f("Description", "string", "description"),
		f("ParentID", "uuid.NullUUID", "parent_id"),
	} {
		ms.Fields = append(ms.Fields, mf)
		ms.ByDB[mf.DBTag] = mf
	}
	return ms
}

func TestGenerateCRUDCompilesAndShapes(t *testing.T) {
	g := &CrudGenerator{Package: "catalogrepo", Receiver: "Repository"}
	body, err := g.GenerateCRUD(categoryTableFixture(), categoryModelFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := format.Source([]byte(body)); err != nil {
		t.Fatalf("generated code does not gofmt/compile: %v\n%s", err, body)
	}

	wants := []string{
		"func (r *Repository) CreateCategory(ctx context.Context, arg CreateCategoryParams) (uuid.UUID, error)",
		"func (r *Repository) GetCategory(ctx context.Context, id uuid.UUID) (Category, error)",
		"func (r *Repository) UpdateCategory(ctx context.Context, id uuid.UUID, arg UpdateCategoryParams) (uuid.UUID, error)",
		"func (r *Repository) DeleteCategory(ctx context.Context, id uuid.UUID) error",
		"ParentID patch.Optional[uuid.NullUUID]",
		"RETURNING \"id\"",
		`if arg.ParentID.Set`,
		"return id, nil",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("generated body missing %q", w)
		}
	}
	if !strings.Contains(body, "ID uuid.UUID") {
		t.Error("CreateCategoryParams should include ID")
	}
}

func TestGenerateCRUDFailsOnMissingModelField(t *testing.T) {
	tbl := categoryTableFixture()
	model := categoryModelFixture()
	delete(model.ByDB, "parent_id")
	if _, err := (&CrudGenerator{Package: "x", Receiver: "Repository"}).GenerateCRUD(tbl, model); err == nil {
		t.Fatal("expected error when a table column has no db-tagged model field")
	}
}

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

func TestGenerateCRUDQualifiedEntity(t *testing.T) {
	g := &CrudGenerator{Package: "orderrepo", Receiver: "Repository",
		ModelPkg: "ordermodel", ModelPath: "shopnexus-server/internal/module/order/model"}
	body, err := g.GenerateCRUD(categoryTableFixture(), categoryModelFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "(ordermodel.Category, error)") {
		t.Fatal("Get must return ordermodel.Category")
	}
	if !strings.Contains(body, `"shopnexus-server/internal/module/order/model"`) {
		t.Fatal("must import model pkg")
	}
}

func TestGenerateFileEmitsList(t *testing.T) {
	g := &CrudGenerator{Package: "orderrepo", Receiver: "Repository",
		ModelPkg: "ordermodel", ModelPath: "shopnexus-server/internal/module/order/model"}
	body, err := g.GenerateFile([]crudItem{{categoryTableFixture(), categoryModelFixture()}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "func (r *Repository) ListCategory(ctx context.Context, f ListCategoryParams) (paginate.PaginateResult[ordermodel.Category], error)") {
		t.Fatalf("missing qualified List signature.\n%s", body)
	}
	if _, err := format.Source([]byte(body)); err != nil {
		t.Fatalf("not gofmt-valid: %v\n%s", err, body)
	}
}

func TestGenerateCRUDQualifiesLocalEnumAndDedupsImports(t *testing.T) {
	tbl := &Table{
		Schema: "order", Name: "transaction",
		Columns: []*Column{
			{Name: "id", Type: "uuid", PrimaryKey: true},
			{Name: "status", Type: "order.order_status"},
			{Name: "date_created", Type: "timestamptz"},
		},
		PrimaryKeys: []*Column{{Name: "id", Type: "uuid", PrimaryKey: true}},
	}
	f := func(n, gt, db string) ModelField { return ModelField{GoName: n, GoType: gt, DBTag: db} }
	m := &ModelStruct{Name: "Transaction", ByDB: map[string]ModelField{},
		Imports: map[string]string{"uuid": "github.com/google/uuid", "time": "time"}}
	for _, mf := range []ModelField{f("ID", "uuid.UUID", "id"), f("Status", "Status", "status"), f("DateCreated", "time.Time", "date_created")} {
		m.Fields = append(m.Fields, mf)
		m.ByDB[mf.DBTag] = mf
	}
	g := &CrudGenerator{Package: "orderrepo", Receiver: "Repository",
		ModelPkg: "ordermodel", ModelPath: "shopnexus-server/internal/module/order/model",
		LocalTypes: map[string]bool{"Transaction": true, "Status": true}}
	body, err := g.GenerateFile([]crudItem{{tbl, m}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := format.Source([]byte(body)); err != nil {
		t.Fatalf("not gofmt-valid: %v\n%s", err, body)
	}
	if !strings.Contains(body, "Status ordermodel.Status") {
		t.Error("Create param must use ordermodel.Status")
	}
	if !strings.Contains(body, "patch.Optional[ordermodel.Status]") {
		t.Error("Update param must use ordermodel.Status")
	}
	if strings.Contains(body, "[]Status") || strings.Contains(body, "[]OrderStatus") {
		t.Error("List must NOT emit an enum IN-filter")
	}
	if strings.Count(body, `"time"`) != 1 {
		t.Errorf("time must be imported exactly once, got %d", strings.Count(body, `"time"`))
	}
}

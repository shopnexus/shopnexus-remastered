package domain_test

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/errx"
)

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func TestNewCategory(t *testing.T) {
	c, err := domain.NewCategory("  Áo thun  ", "Tops", nil)
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	if c.Name != "Áo thun" {
		t.Errorf("name = %q, want it trimmed", c.Name)
	}
	if c.ParentID != nil {
		t.Error("a category built with no parent is a root")
	}
}

func TestNewCategory_Rejects(t *testing.T) {
	long := make([]byte, 101)
	for i := range long {
		long[i] = 'a'
	}
	for _, tc := range []struct{ name, catName, desc string }{
		{name: "no name", catName: "", desc: "x"},
		{name: "name too long", catName: string(long), desc: "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewCategory(tc.catName, tc.desc, nil); status(t, err) != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
		})
	}
}

// A category cannot be its own parent. The wider rule — no cycle anywhere up the chain —
// needs the whole path and is enforced by the write; this is the half the row can see.
func TestCategory_Validate_RefusesSelfParent(t *testing.T) {
	c, err := domain.NewCategory("Tops", "", nil)
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	c.ID = 7
	c.ParentID = &c.ID
	if got := status(t, c.Validate()); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

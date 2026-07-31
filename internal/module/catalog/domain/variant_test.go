package domain_test

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

func variantInput() domain.NewVariantInput {
	return domain.NewVariantInput{
		Price:          299000,
		Attributes:     map[string]any{"size": "l"},
		PackageDetails: map[string]any{"weight_g": 200},
		Quantity:       5,
	}
}

func TestNewVariant(t *testing.T) {
	v, err := domain.NewVariant(variantInput())
	if err != nil {
		t.Fatalf("NewVariant: %v", err)
	}
	if v.Price != 299000 || v.Stock.Quantity != 5 {
		t.Fatalf("variant = %+v", v)
	}
	// A variant is born with its stock row: a purchasable thing with no stock record is
	// not a state anything has to handle.
	if v.Stock.Reserved != 0 || v.Stock.Sold != 0 {
		t.Errorf("stock = %+v, want nothing committed", v.Stock)
	}
}

func TestNewVariant_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*domain.NewVariantInput)
	}{
		{name: "price below one", build: func(in *domain.NewVariantInput) { in.Price = 0 }},
		{name: "negative price", build: func(in *domain.NewVariantInput) { in.Price = -1 }},
		{name: "negative quantity", build: func(in *domain.NewVariantInput) { in.Quantity = -1 }},
		{name: "no attributes", build: func(in *domain.NewVariantInput) { in.Attributes = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := variantInput()
			tc.build(&in)
			if _, err := domain.NewVariant(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

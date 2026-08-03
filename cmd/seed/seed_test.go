package main

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Camisa Denim Top Retro Mujer", "camisa-denim-top-retro-mujer"},
		{"Ropa de Mujer / Tops", "ropa-de-mujer-tops"},
		{"Áo Thun Nữ Đẹp", "ao-thun-nu-dep"},
		{"Perawatan & Kecantikan", "perawatan-kecantikan"},
		{"  --Trailing--  ", "trailing"},
		{"100% Cotton", "100-cotton"},
		{"เสื้อผ้าผู้ชาย", ""}, // no Latin to fold to; the caller drops it
		{"", ""},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestClipSlug(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"one-two-three", 8, "one-two"}, // cut lands mid-word, so back off to the dash
		{"one-two-three", 7, "one-two"}, // cut lands on the dash
		{"abcdefghij", 4, "abcd"},       // no dash to back off to
		{"one-two-three", 4, "one"},     // trailing dash trimmed
	}
	for _, tt := range tests {
		if got := clipSlug(tt.in, tt.n); got != tt.want {
			t.Errorf("clipSlug(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestToInt64(t *testing.T) {
	tests := []struct {
		in   any
		want int64
	}{
		{nil, 0},
		{float64(42.9), 42},
		{"25025", 25025},
		{"1,200 unidades", 1200},
		{"", 0},
		{"agotado", 0},
		{true, 0},
	}
	for _, tt := range tests {
		if got := toInt64(tt.in); got != tt.want {
			t.Errorf("toInt64(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCategoryIndexMatch(t *testing.T) {
	cats, err := loadCategories()
	if err != nil {
		t.Fatal(err)
	}
	idx := newCategoryIndex(cats)

	// Real breadcrumbs from the source dump, one per locale that appears in it.
	tests := []struct {
		name       string
		breadcrumb []string
		want       string
	}{
		{"english leaf", []string{"Home & Living", "Kitchen & Dining"}, "Kitchen & Dining"},
		{"spanish root", []string{"Ropa de Mujer", "Tops", "Camisas y Blusas"}, "Fashion & Clothing"},
		{"spanish accented", []string{"Madre y Bebé", "Ropa de Bebé"}, "Fashion & Clothing"},
		{"indonesian", []string{"Perawatan & Kecantikan", "Perawatan Wajah"}, "Beauty & Personal Care"},
		{"vietnamese", []string{"Thiết Bị Âm Thanh", "Tai Nghe"}, "Electronics"},
		{"thai", []string{"เสื้อผ้าผู้ชาย", "เสื้อยืด"}, "Fashion & Clothing"},
		{"chinese", []string{"保健"}, "Health & Wellness"},
		{"unknown", []string{"Zzz Qqq"}, fallbackCategory.Name},
		{"empty", nil, fallbackCategory.Name},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := idx.match(tt.breadcrumb); got != tt.want {
				t.Errorf("match(%v) = %q, want %q", tt.breadcrumb, got, tt.want)
			}
		})
	}
}

// The plan decides numbers that three schemas later have to agree on, and a database is the
// slowest possible place to find out they do not.
func TestBuildPlanInvariants(t *testing.T) {
	cats, err := loadCategories()
	if err != nil {
		t.Fatal(err)
	}
	products := []product{
		{
			Title: "Camisa Denim Retro", Currency: "MXN", FinalPrice: 868, Rating: 4.7,
			Breadcrumb: []string{"Ropa de Mujer", "Tops"}, Stock: "25025",
			Image:          []string{"https://cdn/a", "https://cdn/a", "https://cdn/b"},
			Specifications: []spec{{"Estilo", "Minimalista"}, {"", "ignored"}},
			Brand:          "Zara",
			Variations:     []variation{{"talla", []string{"S", "M", "L"}}, {"color", []string{"rojo", "azul"}}},
		},
		// Same title, so the slug has to be made unique; no rating, so no sales.
		{Title: "Camisa Denim Retro", Currency: "VND", InitialPrice: 150000, Breadcrumb: []string{"Thời Trang Nữ"}},
		// Nothing sluggable in the title, and no variations at all.
		{Title: "เสื้อผ้า", Currency: "THB", FinalPrice: 200, Rating: 3.2, Breadcrumb: []string{"เสื้อผ้าผู้ชาย"}},
		// Currency the schema would refuse; must be dropped rather than written.
		{Title: "Bad currency", Currency: "$", FinalPrice: 10},
	}

	p := buildPlan(products, cats)
	if len(p.listings) != 3 {
		t.Fatalf("listings = %d, want 3 (the malformed currency dropped)", len(p.listings))
	}
	if len(p.images) != 2 {
		t.Errorf("images = %d, want 2 distinct URLs", len(p.images))
	}

	slugs := map[string]bool{}
	for _, l := range p.listings {
		if slugs[l.slug] {
			t.Errorf("duplicate slug %q", l.slug)
		}
		slugs[l.slug] = true
		if l.slug == "" || len(l.slug) > 100 {
			t.Errorf("slug %q is not storable", l.slug)
		}
		if len(l.variants) == 0 || len(l.variants) > maxVariants {
			t.Errorf("%s: %d variants", l.slug, len(l.variants))
		}

		var sold, sum int64
		featured := 0
		for _, v := range l.variants {
			if len(v.attributes) == 0 {
				t.Errorf("%s: a variant has no attributes", l.slug)
			}
			if v.price < 1 {
				t.Errorf("%s: price %d", l.slug, v.price)
			}
			if v.sold > v.quantity {
				t.Errorf("%s: sold %d exceeds quantity %d", l.slug, v.sold, v.quantity)
			}
			if v.featured {
				featured++
			}
			sold += v.sold
		}
		if featured != 1 {
			t.Errorf("%s: %d featured variants, want 1", l.slug, featured)
		}
		if sold != l.cachedSold {
			t.Errorf("%s: cachedSold %d, variants sold %d", l.slug, l.cachedSold, sold)
		}

		if len(l.sales) > maxReviews {
			t.Errorf("%s: %d sales, more than there are buyers", l.slug, len(l.sales))
		}
		buyers := map[int]bool{}
		for _, s := range l.sales {
			if s.buyer == l.seller {
				t.Errorf("%s: seller bought from itself", l.slug)
			}
			if buyers[s.buyer] {
				t.Errorf("%s: buyer %d reviewed twice", l.slug, s.buyer)
			}
			buyers[s.buyer] = true
			if s.rating < 1 || s.rating > 5 {
				t.Errorf("%s: rating %d out of range", l.slug, s.rating)
			}
			if s.orderedAt.Before(l.createdAt) || s.reviewedAt.Before(s.orderedAt) {
				t.Errorf("%s: listed %s, ordered %s, reviewed %s — out of order",
					l.slug, l.createdAt, s.orderedAt, s.reviewedAt)
			}
			sum += int64(s.rating)
		}
		if int(l.cachedReviewCount) != len(l.sales) {
			t.Errorf("%s: cachedReviewCount %d, sales %d", l.slug, l.cachedReviewCount, len(l.sales))
		}
		// The card's rating has to be the average of the reviews behind it, or the product
		// page contradicts the search result that led to it.
		want := 0.0
		if len(l.sales) > 0 {
			want = float64(sum) / float64(len(l.sales))
		}
		if l.cachedRating != want {
			t.Errorf("%s: cachedRating %v, want %v", l.slug, l.cachedRating, want)
		}
	}

	// A rating is the only evidence in the dump that anybody bought the thing.
	if len(p.listings[1].sales) != 0 {
		t.Errorf("unrated listing got %d sales", len(p.listings[1].sales))
	}
	if len(p.listings[0].sales) == 0 {
		t.Error("rated listing got no sales")
	}
}

// The plan is seeded from a constant so that two runs against two fresh databases produce
// the same rows. Timestamps are the exception and are compared as ages, not instants: a
// listing put up three days ago is three days old whenever the seeder runs.
func TestBuildPlanIsDeterministic(t *testing.T) {
	cats, err := loadCategories()
	if err != nil {
		t.Fatal(err)
	}
	products := []product{
		{Title: "One", Currency: "VND", FinalPrice: 1000, Rating: 4, Breadcrumb: []string{"Beauty"},
			Variations: []variation{{"size", []string{"s", "m"}}}},
		{Title: "Two", Currency: "VND", FinalPrice: 2000, Rating: 2, Breadcrumb: []string{"Watches"}},
	}
	a, b := buildPlan(products, cats), buildPlan(products, cats)
	for i := range a.listings {
		x, y := a.listings[i], b.listings[i]
		if x.slug != y.slug || x.condition != y.condition || x.cachedRating != y.cachedRating {
			t.Fatalf("listing %d differs between runs: %+v vs %+v", i, x, y)
		}
		if len(x.sales) != len(y.sales) {
			t.Fatalf("listing %d: %d sales vs %d", i, len(x.sales), len(y.sales))
		}
		for j := range x.sales {
			a, b := x.sales[j], y.sales[j]
			a.orderedAt, a.reviewedAt = b.orderedAt, b.reviewedAt
			if a != b {
				t.Fatalf("listing %d sale %d differs: %+v vs %+v", i, j, x.sales[j], y.sales[j])
			}
			if got, want := b.reviewedAt.Sub(b.orderedAt), x.sales[j].reviewedAt.Sub(x.sales[j].orderedAt); got != want {
				t.Fatalf("listing %d sale %d: review lag %s vs %s", i, j, got, want)
			}
		}
	}
}

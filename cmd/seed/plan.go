package main

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Everything random in the plan comes from this generator, so two runs of the seeder
// against two fresh databases produce the same listings, the same prices and the same
// reviews. Only the timestamps differ, and they have to: a listing put up three days ago
// is three days old at whichever moment the seeder ran.
const planSeed = 0x5eed_5eed

const (
	// maxVariants caps the cartesian product. Some source products list twenty motifs
	// against six sizes, and a hundred and twenty variants of one listing is a stress test
	// nobody asked this seeder for.
	maxVariants = 20
	maxImages   = 10 // per listing; the gallery is ordered and the first is the cover
	maxTags     = 8
	// maxReviews is not a taste call: a review needs a buyer who is not the seller, and
	// with five accounts there are only four of those. Any more would mean one person
	// reviewing the same listing twice.
	maxReviews = 4
	listingAge = 180 * 24 * time.Hour
)

// plan is the whole seed worked out in memory before a row is written. It exists because
// the numbers cross schemas: "stock"."sold" and "listing"."cached_rating" are decided by
// orders that live in another database entirely, and computing them after the fact would
// mean going back to update rows the catalog transaction had already committed.
type plan struct {
	categories []category
	// images holds each distinct photo URL once, in first-seen order. A resource row is
	// unique on (provider, object_key) and the source repeats URLs both within a product
	// and across them.
	images   []string
	listings []listingPlan
}

type listingPlan struct {
	seller      int // index into seedAccounts
	category    string
	slug        string
	name        string
	description string
	specs       map[string]any
	images      []int // indexes into plan.images
	currency    string
	condition   string
	tags        []string
	createdAt   time.Time
	variants    []variantPlan
	// sales are the completed purchases that back this listing's reviews, and the only
	// reason its cached counters are non-zero.
	sales             []salePlan
	cachedRating      float64
	cachedReviewCount int64
	cachedSold        int64
}

type variantPlan struct {
	price      int64
	attributes map[string]any
	pkg        map[string]any
	featured   bool
	quantity   int64
	sold       int64
}

// salePlan is one completed purchase and the review it earned, planned as one thing
// because trust."review"."order_id" is NOT NULL: no purchase, no review.
type salePlan struct {
	buyer      int // index into seedAccounts
	variant    int // index into the listing's variants
	quantity   int64
	fee        int64 // delivery, which the buyer pays on every sale
	rating     int
	body       string
	orderedAt  time.Time
	reviewedAt time.Time
}

func buildPlan(products []product, cats []category) *plan {
	rng := rand.New(rand.NewPCG(planSeed, planSeed))
	idx := newCategoryIndex(cats)
	now := time.Now()

	p := &plan{categories: cats, images: nil, listings: make([]listingPlan, 0, len(products))}
	imageIdx := map[string]int{}
	slugs := map[string]bool{}

	for i, src := range products {
		l := listingPlan{
			seller:      i % len(seedAccounts),
			category:    idx.match(src.Breadcrumb),
			slug:        uniqueSlug(slugs, src.Title),
			name:        src.Title,
			description: src.Description,
			specs:       planSpecs(src),
			currency:    strings.ToUpper(strings.TrimSpace(src.Currency)),
			condition:   planCondition(rng),
			tags:        planTags(src),
			createdAt:   now.Add(-time.Duration(rng.Int64N(int64(listingAge)))),
		}
		if len(l.currency) != 3 {
			continue // "listing_currency_format" would refuse the row anyway
		}
		for _, url := range src.Image {
			if url == "" || len(l.images) == maxImages {
				continue
			}
			at, ok := imageIdx[url]
			if !ok {
				at = len(p.images)
				imageIdx[url] = at
				p.images = append(p.images, url)
			}
			if !slices.Contains(l.images, at) {
				l.images = append(l.images, at)
			}
		}
		l.variants = planVariants(rng, src)
		planSales(rng, &l, src.Rating, now)
		p.listings = append(p.listings, l)
	}
	return p
}

// planVariants turns the source's option lists into the cartesian product of their values.
func planVariants(rng *rand.Rand, src product) []variantPlan {
	type option struct {
		name   string
		values []string
	}
	var options []option
	seenName := map[string]bool{}
	for _, v := range src.Variations {
		if v.Name == "" || len(v.Variations) == 0 || seenName[v.Name] {
			continue
		}
		seenName[v.Name] = true
		var values []string
		seenValue := map[string]bool{}
		for _, val := range v.Variations {
			if val != "" && !seenValue[val] {
				seenValue[val] = true
				values = append(values, val)
			}
		}
		if len(values) > 0 {
			options = append(options, option{v.Name, values})
		}
	}
	// Truncating every option to two values is the source's own shape kept recognisable —
	// dropping whole options instead would leave a listing that says it comes in three
	// sizes and offers only one.
	total := 1
	for _, o := range options {
		total *= len(o.values)
	}
	if total > maxVariants {
		for i := range options {
			if len(options[i].values) > 2 {
				options[i].values = options[i].values[:2]
			}
		}
	}

	combos := []map[string]any{{}}
	for _, o := range options {
		next := make([]map[string]any, 0, len(combos)*len(o.values))
		for _, base := range combos {
			for _, val := range o.values {
				combo := make(map[string]any, len(base)+1)
				for k, v := range base {
					combo[k] = v
				}
				combo[o.name] = val
				next = append(next, combo)
			}
		}
		combos = next
	}
	if len(combos) == 1 && len(combos[0]) == 0 {
		// domain.Variant requires at least one attribute, and a listing with no options
		// still has to be purchasable.
		combos[0] = map[string]any{"variant": "standard"}
	}

	base := int64(src.FinalPrice)
	if base <= 0 {
		base = int64(src.InitialPrice)
	}
	if base <= 0 {
		base = 1
	}
	perVariant := max(pickStock(rng, src)/int64(len(combos)), 1)

	out := make([]variantPlan, 0, len(combos))
	for i, attrs := range combos {
		// Jitter is a percentage, not an absolute: the eleven source currencies span four
		// orders of magnitude, so ±25 units is invisible in IDR and half the price in SGD.
		price := base + base*int64(rng.IntN(11)-5)/100
		out = append(out, variantPlan{
			price:      max(price, 1),
			attributes: attrs,
			pkg: map[string]any{
				"weight_grams": rng.IntN(1901) + 100,
				"length_cm":    rng.IntN(46) + 5,
				"width_cm":     rng.IntN(46) + 5,
				"height_cm":    rng.IntN(46) + 5,
			},
			featured: i == 0,
			quantity: perVariant,
		})
	}
	return out
}

var stockSpec = regexp.MustCompile(`(?i)existencias|stock|kho`)

// pickStock reads the units on hand out of the scrape, which puts them in "stock" on some
// rows and in a locale-named specification on others. Where it is neither, the seeder
// invents one: every listing being out of stock makes the whole dataset unbuyable.
func pickStock(rng *rand.Rand, src product) int64 {
	if n := toInt64(src.Stock); n > 0 {
		return n
	}
	for _, s := range src.Specifications {
		if stockSpec.MatchString(s.Name) {
			if n := toInt64(s.Value); n > 0 {
				return n
			}
		}
	}
	return int64(rng.IntN(481) + 20)
}

// planSales gives a listing the purchases its source rating implies. A source row with no
// rating gets none — the scrape reports zero reviews throughout, so the rating is the only
// evidence that anybody ever bought the thing.
func planSales(rng *rand.Rand, l *listingPlan, sourceRating float64, now time.Time) {
	if sourceRating <= 0 || len(l.variants) == 0 {
		return
	}
	buyers := rng.Perm(len(seedAccounts))
	count := rng.IntN(maxReviews) + 1

	var sum int64
	for _, buyer := range buyers {
		if len(l.sales) == count {
			break
		}
		if buyer == l.seller {
			continue // a seller does not buy from themselves
		}
		v := rng.IntN(len(l.variants))
		quantity := int64(rng.IntN(3) + 1)
		orderedAt := between(rng, l.createdAt, now)
		rating := min(max(int(sourceRating+rng.Float64()*1.4-0.7+0.5), 1), 5)
		l.sales = append(l.sales, salePlan{
			buyer:      buyer,
			variant:    v,
			quantity:   quantity,
			fee:        max(l.variants[v].price*quantity/20, 1),
			rating:     rating,
			body:       reviewBody(rng, rating),
			orderedAt:  orderedAt,
			reviewedAt: minTime(orderedAt.Add(time.Duration(rng.IntN(14)+1)*24*time.Hour), now),
		})
		l.variants[v].sold += quantity
		l.cachedSold += quantity
		sum += int64(rating)
	}
	// Stock has to cover what was sold: "stock_committed_within_quantity" refuses the row
	// otherwise, and a listing cannot have sold more than it ever had.
	for i := range l.variants {
		l.variants[i].quantity = max(l.variants[i].quantity, l.variants[i].sold+1)
	}
	l.cachedReviewCount = int64(len(l.sales))
	if l.cachedReviewCount > 0 {
		l.cachedRating = float64(sum) / float64(l.cachedReviewCount)
	}
}

func planSpecs(src product) map[string]any {
	out := map[string]any{}
	if src.Brand != "" {
		out["Brand"] = src.Brand
	}
	for _, s := range src.Specifications {
		if s.Name != "" && s.Value != "" {
			out[s.Name] = s.Value
		}
	}
	return out
}

// tagSpecs are the specification names worth promoting to a tag. They are the source's own,
// in its own locales, which is why this is a list and not a rule.
var tagSpecs = map[string]bool{"Estilo": true, "Material": true, "Temporada": true, "Style": true, "Season": true}

func planTags(src product) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		tag := clipSlug(slugify(s), 100)
		if tag == "" || seen[tag] || len(out) == maxTags {
			return
		}
		seen[tag] = true
		out = append(out, tag)
	}
	for _, crumb := range src.Breadcrumb {
		add(crumb)
	}
	add(src.Brand)
	for _, s := range src.Specifications {
		if tagSpecs[s.Name] {
			add(s.Value)
		}
	}
	return out
}

// planCondition spreads the C2C conditions the source has no column for. Weighted towards
// new, because a marketplace where a quarter of everything is damaged is not one anybody
// would demo.
func planCondition(rng *rand.Rand) string {
	switch n := rng.IntN(100); {
	case n < 70:
		return "new"
	case n < 95:
		return "used"
	default:
		return "damaged"
	}
}

func uniqueSlug(taken map[string]bool, title string) string {
	base := clipSlug(slugify(title), 80)
	if base == "" {
		base = "listing"
	}
	slug := base
	for n := 2; taken[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	taken[slug] = true
	return slug
}

func between(rng *rand.Rand, from, to time.Time) time.Time {
	span := to.Sub(from)
	if span <= 0 {
		return from
	}
	return from.Add(time.Duration(rng.Int64N(int64(span))))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

var (
	reviewsPositive = []string{
		"Great product, exactly as described!",
		"Very happy with my purchase. Fast shipping too.",
		"Excellent quality for the price. Would buy again.",
		"Love it! Fits perfectly and looks amazing.",
		"Highly recommend this product to everyone.",
		"Best purchase I've made in a while.",
		"Amazing quality, exceeded my expectations.",
		"Perfect! Just what I was looking for.",
		"Super fast delivery and well packaged.",
		"Five stars! Will definitely order again.",
	}
	reviewsNeutral = []string{
		"Product is okay, nothing special.",
		"Decent quality for the price.",
		"It works as expected.",
		"Average product, meets basic needs.",
		"Not bad, but could be better.",
		"Acceptable quality overall.",
		"Does the job. Nothing more, nothing less.",
		"Reasonable for what you pay.",
	}
	reviewsNegative = []string{
		"Not as expected, quality could be better.",
		"Disappointed with the product.",
		"Could be improved in many ways.",
		"Not worth the price in my opinion.",
		"Had some issues with the product.",
		"Arrived damaged, but seller was helpful.",
		"Smaller than expected from the photos.",
	}
	reviewExtras = []string{
		" The packaging was nice too.",
		" Color matches the picture.",
		" Size is true to description.",
		" Shipping was quicker than I expected.",
		" I've been using it daily since it arrived.",
	}
)

func reviewBody(rng *rand.Rand, rating int) string {
	pool := reviewsNegative
	switch {
	case rating >= 4:
		pool = reviewsPositive
	case rating == 3:
		pool = reviewsNeutral
	}
	body := pool[rng.IntN(len(pool))]
	if rng.IntN(2) == 0 {
		body += reviewExtras[rng.IntN(len(reviewExtras))]
	}
	return body
}

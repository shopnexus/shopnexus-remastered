package main

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// planSeed makes the whole plan deterministic: two runs against two fresh databases produce
// the same listings, the same prices and the same reviews. Only the timestamps differ, and
// they have to — a listing put up three days ago is three days old whenever the seeder ran.
const planSeed = 0x5eed_5eed

// The demo has to be old enough that its history is believable and young enough that the
// background jobs leave it alone. See scenario.go for the second half of that sentence.
const (
	catalogueAge = 150 * 24 * time.Hour
	// minListingAge keeps every listing old enough for a completed sale to fit behind it: a
	// purchase needs three days to arrive and another three for the escrow window, and an
	// order whose completion timestamp is in the future is not a thing the site can show.
	minListingAge = 20 * 24 * time.Hour
	// escrowWindow mirrors the payout job's: a completed order is past it, so its money has
	// reached the seller.
	escrowWindow = 72 * time.Hour
)

type plan struct {
	now        time.Time
	categories []category
	listings   []listingPlan
	// images is every generated listing photo, by object key, in first-seen order. A resource
	// row is unique on (provider, object_key), so the key is the identity.
	images []photo
	// evidence is the handful of photos that are not a listing's: the unboxing receipt, the
	// buyer's refund evidence and a review photo. They live in another module's resource table,
	// so they are kept apart from the gallery.
	evidence []photo
	// photos is the committed photo library, kept so the summary can report how much of the
	// catalogue is real photography and how much fell back to a drawing.
	photos *photoLibrary

	offers  []offerPlan
	orders  []orderPlan
	threads []threadPlan
	tickets []ticketPlan
}

type listingPlan struct {
	seller      string // account key
	category    string
	slug        string
	name        string
	description string
	specs       map[string]any
	condition   string
	priceMode   string
	// featured marks the shop window: the listing a category page opens on, and the one
	// given the fuller trading history.
	featured  bool
	status    string // listing_status: active | pending | hidden
	takedown  string // non-empty makes it a moderator takedown; requires status 'hidden'
	tags      []string
	images    []int // indexes into plan.images
	createdAt time.Time
	variants  []variantPlan

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

// offerPlan is one price negotiation. The row is revised in place by the real service, so the
// plan describes only where it ended up — plus the chat cards that trace how it got there.
type offerPlan struct {
	key       string // stable handle, referenced by orderPlan.offerKey and threadPlan
	listing   int    // index into plan.listings
	variant   int
	buyer     string // account key
	seller    string // account key
	author    string // account key: whose move is on the table
	status    string // offer_status
	quantity  int64
	total     int64
	reason    string
	createdAt time.Time
	expiresAt time.Time
}

// orderState names one of the screens the report has to photograph. Each value fixes the
// whole combination of timestamps, transport status and refund row — the schema derives the
// status from those rather than storing one, so the combination *is* the state.
type orderState string

const (
	stateAwaitingConfirmation orderState = "awaiting-confirmation"
	statePreparing            orderState = "preparing"
	stateInTransit            orderState = "in-transit"
	stateDelivered            orderState = "delivered" // waiting on the buyer to confirm receipt
	stateCompleted            orderState = "completed"
	stateDeclined             orderState = "declined" // the seller refused, with a reason
	stateRefundRequested      orderState = "refund-requested"
	stateRefundDisputed       orderState = "refund-disputed"
	stateRefundAccepted       orderState = "refund-accepted"
)

type orderPlan struct {
	key      string
	buyer    string // account key
	seller   string // account key
	listing  int
	variant  int
	quantity int64
	state    orderState
	// offerKey is set when the sale came from a negotiation rather than a checkout. Exactly
	// one of the two origins exists — the schema's own "order_origin_exactly_one".
	offerKey  string
	createdAt time.Time
	// note is the buyer's message on the line, which the order detail screen renders.
	note   string
	review *reviewPlan
	refund *refundPlan
	// declineReason is only read in stateDeclined.
	declineReason string
}

// reviewPlan is the buyer's review of the goods and everything hanging off it. A review needs
// an order — trust."review"."order_id" is NOT NULL — so it is planned with one.
type reviewPlan struct {
	rating int
	body   string
	// reply is the seller answering, which is what makes the review block on a product page
	// look like a conversation rather than a wall.
	reply string
	// helpful and notHelpful are how many of the other accounts voted. The tally is
	// denormalized onto the review row, so the votes and the counters are planned together.
	helpful    int
	notHelpful int
	at         time.Time
}

type refundPlan struct {
	reason string
	status string // refund_status
	// escalated adds the dispute ticket staff answer. Only meaningful with status 'disputed'.
	escalated bool
	createdAt time.Time
}

// threadPlan is one direct conversation and its messages, one per pair of accounts. A ticket
// thread is not planned here: it is created from `tickets` beside the ticket row itself, which
// is where the requester and the support desk are already paired.
type threadPlan struct {
	a, b     string // account keys, unordered — the writer sorts them by id
	messages []messagePlan
}

// messagePlan is one row in a thread. Exactly one of body/offerKey carries the content: an
// offer card is a system row whose body is never displayed, and an ordinary message is a user
// row with no card.
type messagePlan struct {
	from string // account key; empty means a system row
	body string
	// offerKey renders the negotiation card. The writer turns it into the opaque wire id the
	// client parses, which is why the seeder needs the id cipher key.
	offerKey string
	// listing attaches a reference so the inbox shows the product banner above the thread.
	listing int // 1-based index into plan.listings; 0 means none
	at      time.Time
}

type ticketPlan struct {
	key        string
	requester  string // account key
	kind       string
	subject    string
	refType    string // ticket_ref_type; empty for a feature request
	refOrder   string // order key, when refType is 'order'
	refListing int    // 1-based index into plan.listings, when refType is 'listing'
	reason     string // ticket_reason; report kinds only
	status     string // ticket_status
	assignee   string // account key; required while 'reviewing'
	action     string // ticket_action; set together with the resolution
	resolvedBy string
	note       string
	createdAt  time.Time
	// thread is the conversation opened beside it: the requester's own words first, then the
	// desk answering.
	messages []messagePlan
}

func buildPlan(d *dataset, now time.Time) (*plan, error) {
	rng := rand.New(rand.NewPCG(planSeed, planSeed))
	lib, err := loadPhotoLibrary()
	if err != nil {
		return nil, err
	}
	// The evidence photos stay drawn on purpose. A stock photograph of somebody else's parcel
	// standing in for "what the buyer showed when they opened the box" would be the one piece
	// of seeded data that actively misleads a reader of a dispute.
	p := &plan{now: now, categories: d.Categories, photos: lib, evidence: []photo{
		{key: receiptPhoto, seedText: "bien-nhan-giao-hang", mime: drawnMime},
		{key: refundPhoto, seedText: "bang-chung-hoan-tien", mime: drawnMime},
		{key: reviewPhoto, seedText: "anh-danh-gia-nguoi-mua", mime: drawnMime},
	}}

	slugs := map[string]bool{}
	imageIdx := map[string]int{}
	for _, src := range d.Listings {
		l := listingPlan{
			seller:      src.Seller,
			category:    src.Category,
			slug:        uniqueSlug(slugs, src.Name),
			name:        src.Name,
			description: src.Description,
			specs:       specsOf(src.Specs),
			condition:   src.Condition,
			priceMode:   src.PriceMode,
			featured:    src.Featured,
			status:      "active",
			tags:        src.Tags,
			// Spread over the catalogue's age, so the "newest listings" feed is not one
			// timestamp repeated.
			createdAt: now.Add(-minListingAge - time.Duration(rng.Int64N(int64(catalogueAge-minListingAge)))),
		}
		for i := range src.Images {
			// A real photograph where the library has one for this kind of thing, a drawn
			// placeholder where it does not. The cover is slot zero, so a subject with a
			// single photograph still gets a real card in the browse feed.
			ph := photo{seedText: l.slug, index: i, mime: drawnMime}
			if c, ok := lib.forSubject(src.PhotoSubject, i); ok {
				ph.source, ph.mime = "photos/"+c.File, jpegMime
			}
			ph.key = fmt.Sprintf("seed/listing/%s-%d%s", l.slug, i+1, extFor(ph.mime))
			at, ok := imageIdx[ph.key]
			if !ok {
				at = len(p.images)
				imageIdx[ph.key] = at
				p.images = append(p.images, ph)
			}
			l.images = append(l.images, at)
		}
		for i, v := range src.Variants {
			l.variants = append(l.variants, variantPlan{
				price:      v.Price,
				attributes: attributesOf(v.Attributes),
				pkg:        packageFor(rng),
				featured:   i == 0,
				quantity:   v.Quantity,
			})
		}
		p.listings = append(p.listings, l)
	}

	if err := p.addScenarios(rng); err != nil {
		return nil, err
	}
	p.rollUpCounters()
	return p, nil
}

// rollUpCounters makes the denormalized numbers agree with the rows behind them. They cross
// schemas — catalog's cached_rating is decided by orders in another database — so they are
// computed here rather than after the fact, when the catalog transaction has already committed.
func (p *plan) rollUpCounters() {
	type tally struct {
		sum   int64
		count int64
	}
	byListing := map[int]*tally{}
	for _, o := range p.orders {
		if !o.countsAsSale() {
			continue
		}
		p.listings[o.listing].cachedSold += o.quantity
		p.listings[o.listing].variants[o.variant].sold += o.quantity
		if o.review == nil {
			continue
		}
		t := byListing[o.listing]
		if t == nil {
			t = &tally{}
			byListing[o.listing] = t
		}
		t.sum += int64(o.review.rating)
		t.count++
	}
	for i, t := range byListing {
		p.listings[i].cachedReviewCount = t.count
		p.listings[i].cachedRating = float64(t.sum) / float64(t.count)
	}
	// Stock has to cover what was sold: "stock_committed_within_quantity" refuses the row
	// otherwise, and a listing cannot have sold more than it ever had.
	for i := range p.listings {
		for j := range p.listings[i].variants {
			v := &p.listings[i].variants[j]
			v.quantity = max(v.quantity, v.sold+1)
		}
	}
}

// countsAsSale is whether this order moved units. A declined order never shipped and a
// refunded one came back, so neither is a sale the counters should show.
func (o orderPlan) countsAsSale() bool {
	switch o.state {
	case stateDeclined, stateRefundAccepted:
		return false
	default:
		return true
	}
}

func specsOf(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func attributesOf(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// packageFor invents the parcel a carrier is quoted on. Second-hand goods span a shoebox and a
// bicycle, so the numbers are a range rather than a constant — a courier quote of the same fee
// for every listing is the kind of detail a screenshot shows.
func packageFor(rng *rand.Rand) map[string]any {
	return map[string]any{
		"weight_grams": rng.IntN(4800) + 200,
		"length_cm":    rng.IntN(50) + 10,
		"width_cm":     rng.IntN(40) + 8,
		"height_cm":    rng.IntN(30) + 5,
	}
}

// deliveryFee is what the buyer pays the carrier, and it is not the seller's money: it sits on
// transport.fee and is left out of the escrow release entirely. Distance-free and rounded,
// like every Vietnamese courier's flat intra/inter-province rate.
func deliveryFee(sameProvince bool) int64 {
	if sameProvince {
		return 22000
	}
	return 35000
}

func uniqueSlug(taken map[string]bool, title string) string {
	base := clipSlug(slugify(title), 80)
	if base == "" {
		base = "tin-dang"
	}
	slug := base
	for n := 2; taken[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	taken[slug] = true
	return slug
}

// findListing is how the scenarios name a listing: by a distinctive fragment of its title,
// which survives an edit to the dataset that a numeric index would not.
func (p *plan) findListing(fragment string) (int, error) {
	for i, l := range p.listings {
		if strings.Contains(l.name, fragment) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no listing whose name contains %q", fragment)
}

// extFor keeps the object key's extension honest about the bytes behind it. `local` serves the
// mime off the resource row, but a key that says .png over JPEG bytes is a trap for anybody
// reading the bucket by hand.
func extFor(mime string) string {
	if mime == jpegMime {
		return ".jpg"
	}
	return ".png"
}

// realPhotoCount is how many gallery slots got an actual photograph.
func (p *plan) realPhotoCount() int {
	n := 0
	for _, ph := range p.images {
		if ph.source != "" {
			n++
		}
	}
	return n
}

// evidenceSize is how big the drawn object turned out. The resource row records it, and a row
// claiming zero bytes for an object that has some is a small lie the admin screens repeat.
func (p *plan) evidenceSize(key string) int64 {
	for _, e := range p.evidence {
		if e.key == key {
			return e.size
		}
	}
	return 0
}

func (p *plan) order(key string) (*orderPlan, bool) {
	for i := range p.orders {
		if p.orders[i].key == key {
			return &p.orders[i], true
		}
	}
	return nil, false
}

func (p *plan) offer(key string) (*offerPlan, bool) {
	for i := range p.offers {
		if p.offers[i].key == key {
			return &p.offers[i], true
		}
	}
	return nil, false
}

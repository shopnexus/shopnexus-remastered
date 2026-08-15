package main

import (
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"iPhone 12 64GB bản quốc tế", "iphone-12-64gb-ban-quoc-te"},
		{"Áo Dài Lụa Tơ Tằm", "ao-dai-lua-to-tam"},
		{"Đàn guitar acoustic Yamaha F310", "dan-guitar-acoustic-yamaha-f310"},
		{"  --Trailing--  ", "trailing"},
		{"100% Cotton", "100-cotton"},
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

// The dataset is the only input this command has, and every listing in it names a category and
// a seller by string. A typo in either is a run that fails halfway through writing a database.
func TestDatasetLoads(t *testing.T) {
	d, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Listings) < 30 {
		t.Errorf("%d listings; the demo needs enough to fill a browse page", len(d.Listings))
	}
	for _, l := range d.Listings {
		if len([]rune(l.Description)) < 80 {
			t.Errorf("%q: description is %d characters, too short to read as a person wrote it",
				l.Name, len([]rune(l.Description)))
		}
		if len(l.Specs) == 0 {
			t.Errorf("%q: no specifications", l.Name)
		}
		if l.Images == 0 {
			t.Errorf("%q: no photos", l.Name)
		}
		switch l.Condition {
		case "new", "used", "damaged":
		default:
			t.Errorf("%q: condition %q is not a listing_condition", l.Name, l.Condition)
		}
		switch l.PriceMode {
		case "fixed", "negotiable":
		default:
			t.Errorf("%q: price mode %q is not a price_mode", l.Name, l.PriceMode)
		}
	}
}

// The dump this command replaced had listings called "123123" and "test". A screenshot of one
// of those is the single fastest way to tell a reader the data is made up.
func TestNoPlaceholderNames(t *testing.T) {
	d, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	junk := []string{"test", "asdf", "123123", "sản phẩm a", "abc", "aaa", "xxx"}
	for _, l := range d.Listings {
		name := strings.ToLower(strings.TrimSpace(l.Name))
		for _, bad := range junk {
			if name == bad || strings.HasPrefix(name, bad+" ") {
				t.Errorf("listing named %q", l.Name)
			}
		}
		if len([]rune(l.Name)) < 12 {
			t.Errorf("listing name %q is too short to be a real one", l.Name)
		}
	}
}

// The photo library is the one part of the seed that came from outside, so its provenance is
// checked rather than assumed: a picture whose licence is not one of the two we may use, or
// whose file the manifest names but the tree does not have, must not reach a report.
func TestPhotoLibraryProvenance(t *testing.T) {
	lib, err := loadPhotoLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.credits) == 0 {
		t.Fatal("no photographs at all")
	}
	allowed := map[string]bool{"CC0": true, "Public domain": true}
	seen := map[string]bool{}
	for _, c := range lib.credits {
		if !allowed[c.License] {
			t.Errorf("%s: licence %q is not one of CC0 / Public domain", c.File, c.License)
		}
		if c.SourceURL == "" || c.Title == "" || c.Subject == "" {
			t.Errorf("%s: incomplete provenance (%+v)", c.File, c)
		}
		// The whole point of the change: nothing may come from a marketplace CDN again.
		for _, bad := range []string{"shopee", "lazada", "aliexpress", "amazon.com", "tiki.vn", "susercontent"} {
			if strings.Contains(strings.ToLower(c.SourceURL), bad) {
				t.Errorf("%s: sourced from a marketplace (%s)", c.File, c.SourceURL)
			}
		}
		if seen[c.File] {
			t.Errorf("%s: listed twice in the manifest", c.File)
		}
		seen[c.File] = true
		if _, err := photosFS.ReadFile("photos/" + c.File); err != nil {
			t.Errorf("%s: in the manifest but not in the tree", c.File)
		}
	}
	// And nothing in the tree that the manifest does not account for — an unattributed file is
	// exactly what this manifest exists to make impossible.
	entries, err := photosFS.ReadDir("photos")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jpg") {
			continue
		}
		if !seen[e.Name()] {
			t.Errorf("%s: in the tree with no manifest entry", e.Name())
		}
	}
}

// A gallery slot is either a committed photograph or a drawn placeholder, and its object key
// has to say which: `local` serves the mime off the resource row, and a .png key over JPEG
// bytes is the kind of mismatch that only shows up in a browser.
func TestGallerySlotsAreConsistent(t *testing.T) {
	p := testPlan(t)
	for _, ph := range p.images {
		switch {
		case ph.source != "" && ph.mime != jpegMime:
			t.Errorf("%s: committed photograph declared as %s", ph.key, ph.mime)
		case ph.source == "" && ph.mime != drawnMime:
			t.Errorf("%s: drawn placeholder declared as %s", ph.key, ph.mime)
		}
		if !strings.HasSuffix(ph.key, extFor(ph.mime)) {
			t.Errorf("%s: key extension disagrees with mime %s", ph.key, ph.mime)
		}
	}
	for _, ph := range p.evidence {
		if ph.source != "" {
			t.Errorf("%s: evidence must be drawn, not a stranger's photograph of a parcel", ph.key)
		}
	}

	// The cover is what a browse card shows, so a subject with any photograph at all must use
	// it in slot zero rather than spending it further down the gallery.
	d, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	covered := 0
	for i, l := range d.Listings {
		if len(p.listings[i].images) == 0 {
			t.Fatalf("%s: no gallery at all", l.Name)
		}
		cover := p.images[p.listings[i].images[0]]
		if _, ok := p.photos.forSubject(l.PhotoSubject, 0); ok {
			if cover.source == "" {
				t.Errorf("%s: has a photograph for %q but the cover is drawn", l.Name, l.PhotoSubject)
			}
			covered++
		}
	}
	if covered*2 < len(d.Listings) {
		t.Errorf("only %d of %d listings have a real cover photograph", covered, len(d.Listings))
	}
	t.Logf("%d/%d listings with a real cover; %d/%d gallery slots real",
		covered, len(d.Listings), p.realPhotoCount(), len(p.images))
}

func testPlan(t *testing.T) *plan {
	t.Helper()
	d, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildPlan(d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The plan decides numbers that six schemas later have to agree on, and a database is the
// slowest possible place to find out they do not.
func TestPlanInvariants(t *testing.T) {
	p := testPlan(t)
	now := p.now

	slugs := map[string]bool{}
	for _, l := range p.listings {
		if slugs[l.slug] {
			t.Errorf("duplicate slug %q", l.slug)
		}
		slugs[l.slug] = true
		if l.slug == "" || len(l.slug) > 100 {
			t.Errorf("slug %q is not storable", l.slug)
		}
		if l.createdAt.After(now.Add(-minListingAge)) {
			t.Errorf("%s: listed %s, too recent for a completed sale to fit behind it", l.slug, l.createdAt)
		}
		if l.takedown != "" && l.status != "hidden" {
			t.Errorf("%s: takedown reason on a %s listing", l.slug, l.status)
		}
		featured := 0
		var sold int64
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
	}

	// The card's rating has to be the average of the reviews behind it, or the product page
	// contradicts the search result that led to it.
	type tally struct{ sum, count int64 }
	byListing := map[int]*tally{}
	for _, o := range p.orders {
		if o.review == nil || !o.countsAsSale() {
			continue
		}
		tl := byListing[o.listing]
		if tl == nil {
			tl = &tally{}
			byListing[o.listing] = tl
		}
		tl.sum += int64(o.review.rating)
		tl.count++
	}
	for i, l := range p.listings {
		want := 0.0
		var wantCount int64
		if tl := byListing[i]; tl != nil {
			want = float64(tl.sum) / float64(tl.count)
			wantCount = tl.count
		}
		if l.cachedRating != want || l.cachedReviewCount != wantCount {
			t.Errorf("%s: cached %v over %d, reviews say %v over %d",
				l.slug, l.cachedRating, l.cachedReviewCount, want, wantCount)
		}
	}
}

// Every state fixes a whole combination of timestamps, and the schema has a CHECK for most of
// them. These are the ones it does not, plus the background jobs that would quietly undo a
// state whose clock had already run out.
func TestOrderTimelinesAreLegal(t *testing.T) {
	p := testPlan(t)
	for _, o := range p.orders {
		tl := timelineFor(o)
		switch {
		case tl.receivedAt != nil && tl.confirmedAt == nil:
			t.Errorf("%s: received without a confirmation", o.key)
		case tl.payoutAt != nil && tl.receivedAt == nil:
			t.Errorf("%s: paid out without a receipt", o.key)
		case o.declineReason != "" && (tl.cancelledAt == nil || tl.confirmedAt != nil):
			t.Errorf("%s: a decline has to be a cancellation of an unconfirmed order", o.key)
		}
		for name, at := range map[string]*time.Time{
			"confirmed": tl.confirmedAt, "received": tl.receivedAt,
			"payout": tl.payoutAt, "completed": tl.completedAt, "cancelled": tl.cancelledAt,
		} {
			if at != nil && at.After(p.now) {
				t.Errorf("%s: %s in the future (%s)", o.key, name, at)
			}
		}
		if o.state == stateAwaitingConfirmation && o.createdAt.Before(p.now.Add(-40*time.Hour)) {
			t.Errorf("%s: unanswered for %s; the escalation sweep fires at 48h and would move it",
				o.key, p.now.Sub(o.createdAt))
		}
		if o.refund != nil {
			if o.refund.status == "awaiting-seller-review" &&
				!o.refund.createdAt.Add(sellerReviewWindow).After(p.now) {
				t.Errorf("%s: refund deadline already passed; the overdue sweep would dispute it", o.key)
			}
		}
	}
}

// An order born of a negotiation names the offer, and that offer has to be the one the buyer
// claimed at checkout. Left at 'accepted' with a past expiry, the sweep cancels it and posts a
// chat card saying so.
func TestOffersBackingOrdersAreCheckedOut(t *testing.T) {
	p := testPlan(t)
	used := map[string]int{}
	for _, o := range p.orders {
		if o.offerKey == "" {
			continue
		}
		of, ok := p.offer(o.offerKey)
		if !ok {
			t.Fatalf("%s references missing offer %q", o.key, o.offerKey)
		}
		if of.status != "checked-out" {
			t.Errorf("%s: offer %q is %q, want checked-out", o.key, of.key, of.status)
		}
		if of.buyer != o.buyer || of.seller != o.seller || of.listing != o.listing {
			t.Errorf("%s: offer %q is between different parties or for a different listing", o.key, of.key)
		}
		used[o.offerKey]++
	}
	for key, n := range used {
		if n > 1 {
			t.Errorf("offer %q backs %d orders; item_offer_id_key allows one", key, n)
		}
	}

	// One live negotiation per buyer and variant, which is what the partial unique index says.
	live := map[[2]any]int{}
	for _, of := range p.offers {
		if of.status != "active" {
			continue
		}
		if !of.expiresAt.After(p.now) {
			t.Errorf("offer %q is active but expired; the sweep writes 'cancelled', not 'active'", of.key)
		}
		k := [2]any{of.buyer, [2]int{of.listing, of.variant}}
		live[k]++
		if live[k] > 1 {
			t.Errorf("two live offers from %s on the same variant", of.buyer)
		}
	}
}

// Every chat card has to resolve, or the thread renders "Không thể tải đề nghị giá", and every
// thread has to be between two different accounts.
func TestThreadsAreCoherent(t *testing.T) {
	p := testPlan(t)
	keys := map[string]bool{}
	for _, a := range seedAccounts {
		keys[a.Key] = true
	}
	for _, th := range p.threads {
		if th.a == th.b {
			t.Errorf("thread with oneself: %s", th.a)
		}
		if !keys[th.a] || !keys[th.b] {
			t.Errorf("thread between unknown accounts %q and %q", th.a, th.b)
		}
		for _, m := range th.messages {
			if m.offerKey != "" {
				if _, ok := p.offer(m.offerKey); !ok {
					t.Errorf("message references missing offer %q", m.offerKey)
				}
				continue
			}
			if m.from == "" || !keys[m.from] {
				t.Errorf("message from unknown account %q", m.from)
			}
			if strings.TrimSpace(m.body) == "" {
				t.Error("an ordinary message with an empty body renders as nothing at all")
			}
			if m.listing > len(p.listings) {
				t.Errorf("message references listing %d of %d", m.listing, len(p.listings))
			}
		}
	}
	for _, tk := range p.tickets {
		if len(tk.messages) == 0 {
			t.Errorf("ticket %q has no thread; the support screen would show an empty one", tk.key)
		}
		if tk.status == "reviewing" && tk.assignee == "" {
			t.Errorf("ticket %q is claimed by nobody", tk.key)
		}
		if (tk.status == "resolved") != (tk.action != "") {
			t.Errorf("ticket %q: a resolution and a verdict are the same fact", tk.key)
		}
		if tk.refType == "order" {
			if _, ok := p.order(tk.refOrder); !ok {
				t.Errorf("ticket %q references missing order %q", tk.key, tk.refOrder)
			}
		}
	}
}

// The screens the graduation report has to photograph. Each of these was empty in the data this
// command replaced, and each of them is now written by hand rather than left to a dice roll —
// so this test is the list of what the report is promised, in one place.
func TestReportScenariosExist(t *testing.T) {
	p := testPlan(t)

	t.Run("negotiable listings belong to the shop account", func(t *testing.T) {
		n := 0
		for _, l := range p.listings {
			if l.priceMode == "negotiable" && l.seller == shopKey {
				n++
			}
		}
		if n < 2 {
			t.Errorf("%d negotiable listings on %s; a price negotiation cannot be staged without one", n, shopKey)
		}
	})

	t.Run("negotiable listings have reviews", func(t *testing.T) {
		reviewed, total := 0, 0
		for i, l := range p.listings {
			if l.priceMode != "negotiable" || l.status != "active" {
				continue
			}
			total++
			if l.cachedReviewCount > 0 {
				reviewed++
				continue
			}
			t.Logf("negotiable listing with no reviews: %s", p.listings[i].slug)
		}
		if total == 0 {
			t.Fatal("no negotiable listings at all")
		}
		if reviewed*2 < total {
			t.Errorf("only %d of %d negotiable listings have reviews", reviewed, total)
		}
	})

	t.Run("a live offer from the seller is waiting on the buyer", func(t *testing.T) {
		for _, of := range p.offers {
			if of.status == "active" && of.buyer == buyerKey && of.author == of.seller &&
				of.expiresAt.After(p.now) {
				return
			}
		}
		t.Error("none; the buyer would only ever see a 'rút lại' button")
	})

	t.Run("one order per state on the buyer account", func(t *testing.T) {
		want := []orderState{
			stateAwaitingConfirmation, statePreparing, stateInTransit, stateDelivered,
			stateCompleted, stateDeclined, stateRefundRequested, stateRefundDisputed,
			stateRefundAccepted,
		}
		seen := map[orderState]bool{}
		for _, o := range p.orders {
			if o.buyer == buyerKey {
				seen[o.state] = true
			}
		}
		for _, s := range want {
			if !seen[s] {
				t.Errorf("no %s order on %s", s, buyerKey)
			}
		}
	})

	t.Run("a refund is still inside the seller's window", func(t *testing.T) {
		for _, o := range p.orders {
			if o.refund != nil && o.refund.status == "awaiting-seller-review" {
				return
			}
		}
		t.Error("none; the 48h clock is never on screen")
	})

	t.Run("an open dispute ticket is waiting on a verdict", func(t *testing.T) {
		for _, tk := range p.tickets {
			if tk.kind == "refund-dispute" && tk.status != "resolved" {
				return
			}
		}
		t.Error("none; the moderator queue photographs as an empty state")
	})

	t.Run("every seller has a reputation to show", func(t *testing.T) {
		sells := map[string]bool{}
		rated := map[string]bool{}
		for i, l := range p.listings {
			if l.status != "active" {
				continue
			}
			sells[l.seller] = true
			if p.listings[i].cachedReviewCount > 0 {
				rated[l.seller] = true
			}
		}
		for seller := range sells {
			if !rated[seller] {
				t.Errorf("%s sells but has no reviewed listing; the shop header reads 0.0 ★", seller)
			}
		}
	})
}

// A withdrawal may not take more than the balance ever holds after it, or the ledger's
// non-negative CHECK refuses a row that the seeder has already counted on.
func TestMinAvailableFrom(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	moves := []move{
		{account: "a", availableDelta: 1000, at: base},
		{account: "b", availableDelta: 9999, at: base.Add(time.Hour)},
		{account: "a", availableDelta: -400, at: base.Add(2 * time.Hour)},
		{account: "a", availableDelta: 250, at: base.Add(3 * time.Hour)},
	}
	if got := minAvailableFrom(moves, "a", base); got != 600 {
		t.Errorf("from the start = %d, want 600 (the dip after the debit)", got)
	}
	if got := minAvailableFrom(moves, "a", base.Add(3*time.Hour)); got != 850 {
		t.Errorf("from the last movement = %d, want 850", got)
	}
	if got := minAvailableFrom(moves, "c", base); got != 0 {
		t.Errorf("unknown account = %d, want 0", got)
	}
}

func TestRoundDown(t *testing.T) {
	for _, tt := range []struct{ in, step, want int64 }{
		{7_349_000, 500_000, 7_000_000},
		{499_999, 500_000, 0},
		{1_000_000, 500_000, 1_000_000},
		{42, 0, 42},
	} {
		if got := roundDown(tt.in, tt.step); got != tt.want {
			t.Errorf("roundDown(%d, %d) = %d, want %d", tt.in, tt.step, got, tt.want)
		}
	}
}

// The plan is seeded from a constant so that two runs against two fresh databases produce the
// same rows. Timestamps are the exception and are compared as ages, not instants.
func TestPlanIsDeterministic(t *testing.T) {
	d, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a, err := buildPlan(d, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildPlan(d, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.orders) != len(b.orders) || len(a.offers) != len(b.offers) {
		t.Fatalf("run 1 has %d orders / %d offers, run 2 has %d / %d",
			len(a.orders), len(a.offers), len(b.orders), len(b.offers))
	}
	for i := range a.listings {
		x, y := a.listings[i], b.listings[i]
		if x.slug != y.slug || x.cachedRating != y.cachedRating || x.cachedSold != y.cachedSold {
			t.Fatalf("listing %d differs between runs: %+v vs %+v", i, x, y)
		}
		if !x.createdAt.Equal(y.createdAt) {
			t.Fatalf("listing %d: listed %s vs %s", i, x.createdAt, y.createdAt)
		}
	}
	for i := range a.orders {
		x, y := a.orders[i], b.orders[i]
		if x.key != y.key || x.state != y.state || x.buyer != y.buyer || x.listing != y.listing {
			t.Fatalf("order %d differs between runs: %+v vs %+v", i, x, y)
		}
	}
}

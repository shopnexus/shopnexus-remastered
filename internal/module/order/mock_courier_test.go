package order_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/order"
	orderapi "shopnexus/internal/module/order/api"
	transportmock "shopnexus/internal/provider/transport/mock"
)

// The mock courier's behaviour lives in Go and the rows a buyer picks from live in SQL, so the two
// can drift: a row nobody implements silently behaves like standard delivery, and a scenario nobody
// seeded is unreachable. Neither shows up as a failure anywhere else.
func TestMockTransportOptions_EveryScenarioIsSeededAndNothingElseIs(t *testing.T) {
	sql := readMigrations(t, order.Migrations())

	for _, slug := range transportmock.ScenarioIDs() {
		if !strings.Contains(sql, "'"+slug+"'") {
			t.Errorf("scenario %q is implemented but no migration seeds it, so nobody can pick it", slug)
		}
	}
	for _, slug := range mockSlugsIn(sql) {
		if !contains(transportmock.ScenarioIDs(), slug) {
			t.Errorf("migration seeds %q but the courier does not implement it, so it behaves like standard delivery", slug)
		}
	}
}

func readMigrations(t *testing.T, dir fs.FS) string {
	t.Helper()
	var b strings.Builder
	err := fs.WalkDir(dir, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(dir, path)
		if err != nil {
			return err
		}
		b.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	return b.String()
}

// mockSlugsIn picks the quoted `mock-…` literals out of the SQL. Crude on purpose: the alternative
// is parsing SQL, and the only quoted mock- literals in a migration are these ids.
func mockSlugsIn(sql string) []string {
	var out []string
	for rest := sql; ; {
		i := strings.Index(rest, "'mock-")
		if i < 0 {
			return out
		}
		rest = rest[i+1:]
		end := strings.IndexByte(rest, '\'')
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
		rest = rest[end:]
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A carrier row naming a vendor this deployment is not configured for is a claim the database
// cannot keep: the parcel would be booked with a courier nothing can reach. It is missing from the
// quote list, and choosing it at checkout is refused with the same 422 as a slug nobody enabled.
func TestCarriers_ACourierThisDeploymentCannotReachIsNotOffered(t *testing.T) {
	h := newHarness("fixed")
	h.repo.options = append(h.repo.options, common.Option{
		ID: "ghtk-standard", Name: "GHTK", Type: common.OptionTypeTransport,
		IsEnabled: true, Provider: "ghtk",
	})
	ctx := context.Background()

	quotes, err := h.svc.ShippingQuotes(ctx, orderapi.ShippingQuotesRequest{
		ActorID: buyer, VariantID: variantID,
	})
	if err != nil {
		t.Fatalf("ShippingQuotes: %v", err)
	}
	for _, o := range quotes.Options {
		if o.Option == "ghtk-standard" {
			t.Fatal("a carrier for another vendor is quoted, so a buyer can pay for a parcel nothing will book")
		}
	}

	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{ActorID: buyer, ListingID: listingID})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if got := status(t, mustErr(h.svc.Checkout(ctx, orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghtk-standard",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 1}},
	}))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

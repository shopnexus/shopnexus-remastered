package catalog_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/catalog"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/order"
	"shopnexus/internal/shared/id"
)

// RecordInteractions is fire-and-forget: it publishes one fact per interaction and answers
// before anything downstream reads it. This is the service half of that contract; the
// subscriber that turns a fact into a listing_signal row lives in fx.go, and
// TestListingSignals_MoveInterests below exercises the repository arithmetic it feeds
// directly — the async hand-off between the two is the bus's job, already covered in
// internal/infra/eventbus.
func TestRecordInteractions_PublishesOnePerAction(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer := id.Of[id.Account](82)

	var got []catalog.ListingInteraction
	eventbus.Subscribe(h.bus, catalog.ListingInteractionTopic, "test",
		func(_ context.Context, e catalog.ListingInteraction) error {
			got = append(got, e)
			return nil
		})

	if err := h.svc.RecordInteractions(ctx, catalogapi.RecordInteractionsRequest{
		ActorID: buyer,
		Interactions: []catalogapi.InteractionInput{
			{ListingID: listing.ID, Type: catalogapi.InteractionView},
			{ListingID: listing.ID, Type: catalogapi.InteractionClickFromSearch},
		},
	}); err != nil {
		t.Fatalf("RecordInteractions: %v", err)
	}
	h.bus.Wait()

	if len(got) != 2 {
		t.Fatalf("published = %d, want 2 — one per interaction in the batch", len(got))
	}
	if got[0].AccountID != buyer.Int64() || got[0].ListingID != listing.ID.Int64() ||
		got[0].Type != catalogapi.InteractionView {
		t.Errorf("first event = %+v", got[0])
	}
	if got[1].Type != catalogapi.InteractionClickFromSearch {
		t.Errorf("second event = %+v, want click-from-search", got[1])
	}
}

// The wiring the fx graph would otherwise be the only thing to exercise: a published fact
// reaches SubscribeListingInteractions' own subscription and lands as a listing_signal row,
// anonymous actions dropped rather than written with an account id of 0. Slow-ish (it waits
// out the subscriber's own 2s linger, since nothing here can shorten it), so kept to this one
// test rather than run under every scenario above.
func TestSubscribeListingInteractions_WritesSignal(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer := id.Of[id.Account](83)

	catalog.SubscribeListingInteractions(h.bus, h.repo, slog.New(slog.DiscardHandler))

	if err := h.svc.RecordInteractions(ctx, catalogapi.RecordInteractionsRequest{
		ActorID: buyer,
		Interactions: []catalogapi.InteractionInput{
			{ListingID: listing.ID, Type: catalogapi.InteractionView},
		},
	}); err != nil {
		t.Fatalf("RecordInteractions: %v", err)
	}
	// Anonymous: no account for personalisation to attach to, so this one must not appear.
	if err := h.svc.RecordInteractions(ctx, catalogapi.RecordInteractionsRequest{
		Interactions: []catalogapi.InteractionInput{
			{ListingID: listing.ID, Type: catalogapi.InteractionView},
		},
	}); err != nil {
		t.Fatalf("RecordInteractions (anonymous): %v", err)
	}
	h.bus.Wait()

	if len(h.repo.signals) != 1 {
		t.Fatalf("listing_signal rows = %d, want 1 (the anonymous one dropped)", len(h.repo.signals))
	}
	got := h.repo.signals[0]
	if got.accountID != buyer.Int64() || got.listingID != listing.ID.Int64() ||
		got.signalType != catalogapi.InteractionView {
		t.Errorf("signal = %+v", got)
	}
}

// A completed sale is the strongest signal this module ever writes, and it arrives on order's
// own fact rather than a route of this module's — SubscribeOrderPlaced is what turns one line
// into one listing_signal row for the buyer, never the seller.
func TestSubscribeOrderPlaced_WritesPurchaseSignal(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer, seller := id.Of[id.Account](84), id.Of[id.Account](85)

	catalog.SubscribeOrderPlaced(h.bus, h.repo, slog.New(slog.DiscardHandler))

	if err := eventbus.Publish(context.Background(), h.bus, order.OrderPlacedTopic, order.OrderPlaced{
		OrderID: 1, BuyerID: buyer.Int64(), SellerID: seller.Int64(), Total: 100000, Currency: "VND",
		Lines: []order.OrderLine{{ListingID: listing.ID.Int64(), VariantID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	h.bus.Wait()

	if len(h.repo.signals) != 1 {
		t.Fatalf("listing_signal rows = %d, want 1", len(h.repo.signals))
	}
	got := h.repo.signals[0]
	if got.accountID != buyer.Int64() || got.listingID != listing.ID.Int64() ||
		got.signalType != catalogapi.InteractionPurchase {
		t.Errorf("signal = %+v, want the buyer's purchase of the listing", got)
	}
}

// A view or a click, not just a save, moves what the personalised feed believes an account is
// into — the whole reason listing_signal exists next to favorite. The signal is written here
// through the repository directly: reaching it through the bus would test the bus, which
// internal/infra/eventbus already does.
func TestListingSignals_MoveInterests(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	viewed := seedListing(t, h)
	publish(t, h, viewed)
	buyer := id.Of[id.Account](81)

	if err := h.repo.InsertListingSignals(ctx, []port.ListingSignal{
		{AccountID: buyer.Int64(), ListingID: viewed.ID.Int64(), Type: catalogapi.InteractionView},
	}); err != nil {
		t.Fatalf("InsertListingSignals: %v", err)
	}

	// A signal alone triggers no write — only a favorite write or the sweep recomputes — so
	// there is nothing to read back yet. This is the arithmetic under test, not the trigger.
	if interests, _ := h.repo.Interests(ctx, buyer.Int64()); len(interests) != 0 {
		t.Fatalf("interests = %+v, want none before a recompute runs", interests)
	}

	// A favorite write on anything recomputes from everything the account has, the signal
	// above included.
	saved := seedListingNamed(t, h, "Saved separately")
	publish(t, h, saved)
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: saved.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	interests, err := h.repo.Interests(ctx, buyer.Int64())
	if err != nil {
		t.Fatalf("Interests: %v", err)
	}
	if len(interests) != 2 {
		t.Fatalf("interests = %+v, want one slot for the viewed listing's category and one for the saved one's", interests)
	}
}

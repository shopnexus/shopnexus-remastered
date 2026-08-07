package trust_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/api/catalogtest"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/api/chattest"
	"shopnexus/internal/module/common"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/api/ordertest"
	"shopnexus/internal/module/trust"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

const (
	buyer     = id.ID[id.Account](1)
	seller    = id.ID[id.Account](2)
	stranger  = id.ID[id.Account](3)
	moderator = id.ID[id.Account](4)
	orderID   = id.ID[id.Order](10)
	listingID = id.ID[id.Listing](20)
)

// fakeAccounts answers the caller's role and the names beside a review or a report.
type fakeAccounts struct {
	accounttest.Stub
	// roles is per account, so one harness can serve a moderator and an ordinary user.
	roles map[id.ID[id.Account]]string
	// missing is an account that does not exist, which is what makes a report against
	// nothing a 404.
	missing map[id.ID[id.Account]]bool
}

func (f fakeAccounts) GetMe(_ context.Context, req accountapi.GetMeRequest) (accountapi.Me, error) {
	role := f.roles[req.ActorID]
	if role == "" {
		role = "user"
	}
	return accountapi.Me{Role: role}, nil
}

func (f fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	if f.missing[req.ID] {
		return accountapi.PublicAccount{}, errx.NewError(404, "account_not_found", "account not found")
	}
	return accountapi.PublicAccount{ID: req.ID, Name: "Somebody"}, nil
}

// fakeCatalog owns the listing and records what rating trust pushed into its cache.
type fakeCatalog struct {
	catalogtest.Stub
	// synced is the last average and count handed over, per listing.
	synced map[int64][2]float64
	// missing hides the listing, which is what a report against a deleted one hits — and what
	// a listing back in `pending` looks like to its own buyer.
	missing bool
	// getErr is the other way a read fails: the module is there but unreachable, which is not
	// the same answer as "it does not exist".
	getErr error
}

func (f *fakeCatalog) GetListing(_ context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	if f.getErr != nil {
		return catalogapi.ListingDetail{}, f.getErr
	}
	if f.missing {
		return catalogapi.ListingDetail{}, errx.NewError(404, "listing_not_found", "listing not found")
	}
	return catalogapi.ListingDetail{
		ID: req.ID, Name: "Ao thun", Status: "active",
		Seller: accountapi.AccountSummary{ID: seller},
	}, nil
}

func (f *fakeCatalog) SyncListingRating(_ context.Context, req catalogapi.SyncListingRatingRequest) error {
	f.synced[req.ListingID.Int64()] = [2]float64{req.Rating, float64(req.Count)}
	return nil
}

// fakeOrders is the sale behind a rating: who the parties were, whether it is finished, and
// what it covered.
type fakeOrders struct {
	ordertest.Stub
	state string
	// listing is what the order's single line bought; zero means it bought something else.
	listing id.ID[id.Listing]
	// escalateErr is order refusing the escalation — the wrong party, or a refund past the point
	// where staff can still decide it.
	escalateErr error
	// escalated counts the sales handed to staff, which is what says the ticket did not open one
	// order knows nothing about.
	escalated []id.ID[id.Order]
}

func (f *fakeOrders) GetRefund(_ context.Context, req orderapi.RefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{ID: req.ID, OrderID: orderID, BuyerID: buyer}, nil
}

func (f *fakeOrders) EscalateRefund(_ context.Context, req orderapi.EscalateRefundRequest) (orderapi.Refund, error) {
	if f.escalateErr != nil {
		return orderapi.Refund{}, f.escalateErr
	}
	f.escalated = append(f.escalated, req.OrderID)
	return orderapi.Refund{ID: id.ID[id.Refund](55), OrderID: req.OrderID, BuyerID: buyer}, nil
}

func (f *fakeOrders) GetOrder(_ context.Context, req orderapi.OrderRequest) (orderapi.Order, error) {
	// The order module answers 404 for somebody who is not a party, so a stranger never
	// reaches the direction check.
	if req.ActorID != buyer && req.ActorID != seller {
		return orderapi.Order{}, errx.NewError(404, "order_not_found", "order not found")
	}
	listing := f.listing
	if listing == 0 {
		listing = listingID
	}
	return orderapi.Order{
		ID:     req.ID,
		Buyer:  accountapi.AccountSummary{ID: buyer},
		Seller: accountapi.AccountSummary{ID: seller},
		State:  f.state,
		Items:  []orderapi.Item{{ListingID: listing, SellerID: seller}},
	}, nil
}

type fakeChat struct {
	chattest.Stub
	// missing hides the message, which is what a report against a deleted one hits.
	missing bool
	// threads is one conversation per ticket, so opening the same one twice answers the same id —
	// the idempotence trust relies on when it repairs a ticket whose thread never landed.
	threads map[id.ID[id.Ticket]]id.ID[id.Conversation]
	// openErr is chat being unreachable at the moment a ticket is filed.
	openErr error
	// opened is what the requester wrote, so a test can see the body became the first message.
	opened []chatapi.OpenTicketThreadRequest
	// posted is what another module put into a ticket thread — a refund verdict.
	posted []chatapi.PostTicketMessageRequest
}

func (f *fakeChat) PostTicketMessage(_ context.Context, req chatapi.PostTicketMessageRequest) (chatapi.Message, error) {
	f.posted = append(f.posted, req)
	return chatapi.Message{ConversationID: f.threads[req.TicketID]}, nil
}

func (f *fakeChat) OpenTicketThread(_ context.Context, req chatapi.OpenTicketThreadRequest) (chatapi.Conversation, error) {
	if f.openErr != nil {
		return chatapi.Conversation{}, f.openErr
	}
	f.opened = append(f.opened, req)
	if got, ok := f.threads[req.TicketID]; ok {
		return chatapi.Conversation{ID: got, TicketID: &req.TicketID}, nil
	}
	next := id.ID[id.Conversation](int64(len(f.threads) + 1))
	f.threads[req.TicketID] = next
	return chatapi.Conversation{ID: next, TicketID: &req.TicketID}, nil
}

func (f *fakeChat) GetMessage(_ context.Context, req chatapi.GetMessageRequest) (chatapi.Message, error) {
	if f.missing {
		return chatapi.Message{}, errx.NewError(404, "message_not_found", "message not found")
	}
	return chatapi.Message{ID: req.ID, Body: "hello"}, nil
}

type harness struct {
	svc      *trust.Service
	repo     *fakeRepo
	catalog  *fakeCatalog
	orders   *fakeOrders
	chat     *fakeChat
	accounts fakeAccounts
	// uploads is the two-step upload seam; images is its confirmed set. A test that attaches a
	// photo has to declare it, which is what makes ErrAttachmentNotFound reachable — and an
	// unconfirmed id resolves to nothing, exactly as the real store leaves it.
	uploads *fakeUploads
	images  map[int64]bool
}

func newHarness(state string) *harness {
	repo := newFakeRepo()
	catalog := &fakeCatalog{synced: map[int64][2]float64{}}
	orders := &fakeOrders{state: state}
	chat := &fakeChat{threads: map[id.ID[id.Ticket]]id.ID[id.Conversation]{}}
	accounts := fakeAccounts{
		roles:   map[id.ID[id.Account]]string{moderator: "moderator"},
		missing: map[id.ID[id.Account]]bool{},
	}
	uploads := newFakeUploads()
	svc := trust.NewService(repo, accounts, catalog, orders, chat, uploads,
		validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo, catalog: catalog, orders: orders, chat: chat,
		accounts: accounts, uploads: uploads, images: uploads.confirmed}
}

// status is the status the gateway would answer. It translates a validator error the way the edge
// does, so a test can pin a field the contract refuses as the 400 a client actually receives.
func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(validation.AsError(err))
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func mustErr[T any](_ T, err error) error { return err }

// ---------------------------------------------------------------- feedback ---

// The blind window in one test: one side's rating is hidden from the other, submitting the
// second reveals both, and only then does either count towards a reputation.
func TestFeedback_BlindUntilBothSidesRate(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()

	mine, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: buyer, OrderID: orderID, Rating: 5, Comment: "fast",
	})
	if err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	// The direction follows from which side of the order the caller is on, never from the
	// body: nobody can file feedback as the other party.
	if mine.Direction != domain.DirectionBuyerToSeller || mine.RateeID != seller {
		t.Fatalf("feedback = %+v, want the buyer rating the seller", mine)
	}
	if mine.PublishedAt != nil {
		t.Error("a first rating is visible, so the second side can retaliate")
	}
	// And nothing is counted while it is blind.
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.RatingCount != 0 {
		t.Errorf("rating count = %d, want a blind rating uncounted", rep.RatingCount)
	}

	// The seller sees that something is waiting and when, but not what it is.
	view, err := h.svc.GetOrderFeedback(ctx, trustapi.OrderFeedbackRequest{ActorID: seller, OrderID: orderID})
	if err != nil {
		t.Fatalf("GetOrderFeedback: %v", err)
	}
	if view.Mine != nil || view.Theirs != nil || !view.TheirsSubmitted {
		t.Fatalf("view = %+v, want a pending rating the seller cannot read", view)
	}
	if view.RevealAt == nil {
		t.Error("no reveal time, so a client cannot count down")
	}

	// The seller answers, and both reveal at once.
	if _, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: seller, OrderID: orderID, Rating: 4,
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	view, err = h.svc.GetOrderFeedback(ctx, trustapi.OrderFeedbackRequest{ActorID: seller, OrderID: orderID})
	if err != nil {
		t.Fatalf("GetOrderFeedback: %v", err)
	}
	if view.Mine == nil || view.Theirs == nil {
		t.Fatalf("view = %+v, want both sides visible", view)
	}
	if view.RevealAt != nil {
		t.Error("a reveal time is still set with nothing left to wait for")
	}
	// Publishing is what counts a rating, in both directions.
	rep, err = h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.RatingCount != 1 || rep.RatingAverage != 5 {
		t.Errorf("seller reputation = %+v, want one 5", rep)
	}
	rep, err = h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: buyer, Role: "buyer"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.RatingCount != 1 || rep.RatingAverage != 4 {
		t.Errorf("buyer reputation = %+v, want one 4", rep)
	}
}

// One submission per direction, so a rating cannot be revised after reading the other side.
func TestFeedback_OnePerDirection(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	req := trustapi.SubmitFeedbackRequest{ActorID: buyer, OrderID: orderID, Rating: 5}
	if _, err := h.svc.SubmitFeedback(ctx, req); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	if got := status(t, mustErr(h.svc.SubmitFeedback(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// A rating on a sale that is still running rates something that has not happened; a stranger
// is not a party to it at all.
func TestFeedback_NeedsAFinishedOrderAndAParty(t *testing.T) {
	h := newHarness("open")
	ctx := context.Background()
	if got := status(t, mustErr(h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: buyer, OrderID: orderID, Rating: 5,
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 while the order is open", got)
	}
	h.orders.state = "completed"
	if got := status(t, mustErr(h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: stranger, OrderID: orderID, Rating: 5,
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for somebody else's order", got)
	}
}

// The window is what stops a party who never rates from hiding the other's rating for ever.
func TestRevealDueFeedback_ClosesTheWindow(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	mine, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: buyer, OrderID: orderID, Rating: 5,
	})
	if err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	if revealed, err := h.svc.RevealDueFeedback(ctx, 10); err != nil || revealed != 0 {
		t.Fatalf("RevealDueFeedback = %d, %v; want nothing due yet", revealed, err)
	}
	// Wind the submission back past the window, as the clock would.
	stored := h.repo.feedback[mine.ID.Int64()]
	stored.CreatedAt = stored.CreatedAt.Add(-domain.BlindWindow - time.Hour)
	h.repo.feedback[mine.ID.Int64()] = stored

	revealed, err := h.svc.RevealDueFeedback(ctx, 10)
	if err != nil {
		t.Fatalf("RevealDueFeedback: %v", err)
	}
	if revealed != 1 {
		t.Fatalf("revealed = %d, want the stale rating out", revealed)
	}
	// It counted exactly once, and a retried pass counts nothing more.
	if revealed, err := h.svc.RevealDueFeedback(ctx, 10); err != nil || revealed != 0 {
		t.Fatalf("second pass = %d, %v; want nothing left", revealed, err)
	}
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.RatingCount != 1 {
		t.Fatalf("rating count = %d, want the reveal counted once", rep.RatingCount)
	}
}

// A ratee's published history pages on (created_at, id) like the rest, so two ratings that
// landed in the same instant are both reachable.
func TestListAccountFeedback_PagesWithoutSkipping(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	for _, order := range []id.ID[id.Order]{orderID, id.Of[id.Order](11)} {
		for _, actor := range []id.ID[id.Account]{buyer, seller} {
			if _, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
				ActorID: actor, OrderID: order, Rating: 5,
			}); err != nil {
				t.Fatalf("SubmitFeedback: %v", err)
			}
		}
	}
	// One transaction's rows share created_at exactly.
	for rowID, row := range h.repo.feedback {
		row.CreatedAt = at
		h.repo.feedback[rowID] = row
	}

	seen := map[id.ID[id.Feedback]]bool{}
	cursor := ""
	for range 3 {
		page, err := h.svc.ListAccountFeedback(ctx, trustapi.ListFeedbackRequest{
			AccountID: seller, Role: "seller", Cursor: cursor, Limit: 1,
		})
		if err != nil {
			t.Fatalf("ListAccountFeedback: %v", err)
		}
		for _, row := range page.Data {
			seen[row.ID] = true
		}
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	if len(seen) != 2 {
		t.Fatalf("history = %v, want both ratings of the seller across the pages", seen)
	}
}

// The two rating pairs stay apart, and the order counters come from order's own event.
func TestReputation_KeepsTheTwoRatingsApart(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	if _, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: buyer, OrderID: orderID, Rating: 5,
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	if _, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: seller, OrderID: orderID, Rating: 5,
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	if _, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 1, Body: "not as described",
	}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if err := h.svc.RecordOrderOutcome(ctx, trustapi.RecordOrderOutcomeRequest{
		OrderID: orderID, BuyerID: buyer, SellerID: seller, Completed: true,
	}); err != nil {
		t.Fatalf("RecordOrderOutcome: %v", err)
	}

	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	// One order produced both a 5 on the transaction and a 1 on the goods. Summing them
	// would count that order twice and answer 3.
	if rep.RatingAverage != 5 || rep.RatingCount != 1 {
		t.Errorf("transaction rating = %v over %d, want one 5", rep.RatingAverage, rep.RatingCount)
	}
	if rep.ReviewRatingAverage != 1 || rep.ReviewRatingCount != 1 {
		t.Errorf("review rating = %v over %d, want one 1", rep.ReviewRatingAverage, rep.ReviewRatingCount)
	}
	if rep.CompletedOrders != 1 || rep.CancelledOrders != 0 {
		t.Errorf("order counters = %d/%d, want one completed", rep.CompletedOrders, rep.CancelledOrders)
	}
	// A buyer has no review rating: nobody reviews a buyer's products.
	buyerRep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: buyer, Role: "buyer"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if buyerRep.ReviewRatingCount != 0 {
		t.Errorf("buyer review count = %d, want none", buyerRep.ReviewRatingCount)
	}
}

// ----------------------------------------------------------------- reviews ---

// No purchase, no review: the order is what earns it, one per (listing, order), and the
// seller's cached rating follows.
func TestSubmitReview_NeedsThePurchase(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	req := trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4, Body: "good",
	}
	got, err := h.svc.SubmitReview(ctx, req)
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got.Rating != 4 || got.Author.ID != buyer {
		t.Fatalf("review = %+v, want the buyer's 4", got)
	}
	// The cached rating catalog renders was handed over, with the count behind it.
	if synced := h.catalog.synced[listingID.Int64()]; synced != [2]float64{4, 1} {
		t.Errorf("synced = %v, want a 4 over one review", synced)
	}
	// One per order.
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// The seller does not review their own goods, and a stranger has no order at all.
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: seller, ListingID: listingID, OrderID: orderID, Rating: 5,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for the seller", got)
	}
	// And an order that did not carry the listing earns no review of it.
	h.orders.listing = id.Of[id.Listing](99)
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5,
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 for a listing the order did not include", got)
	}
}

// A review rates goods the buyer kept. A cancelled sale — one refunded in full — leaves none,
// and its items keep CancelledAt nil once they belong to an order, so the per-item check cannot
// see it. Rating the counterparty of that sale is still legitimate, which is feedback's job.
func TestSubmitReview_NeedsACompletedOrder(t *testing.T) {
	h := newHarness("cancelled")
	ctx := context.Background()
	req := trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 1, Body: "returned it",
	}
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, req))); got != 422 {
		t.Fatalf("status = %d, want 422 for a cancelled sale", got)
	}
	// The same order still earns feedback about the seller.
	if _, err := h.svc.SubmitFeedback(ctx, trustapi.SubmitFeedbackRequest{
		ActorID: buyer, OrderID: orderID, Rating: 1, Comment: "never shipped",
	}); err != nil {
		t.Fatalf("SubmitFeedback on a cancelled order: %v", err)
	}
	// And a sale that is still running earns neither.
	h.orders.state = "open"
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, req))); got != 422 {
		t.Fatalf("status = %d, want 422 while the order is open", got)
	}
	h.orders.state = "completed"
	if _, err := h.svc.SubmitReview(ctx, req); err != nil {
		t.Fatalf("SubmitReview on a completed order: %v", err)
	}
}

// The seller a rating counts towards is frozen on the review, so editing or deleting one does
// not depend on the listing still being readable. It is not: a seller who re-publishes a
// listing puts it back in `pending`, which answers 404 even to the buyer who bought it — and
// the old code turned that into a rating filed against account 0.
func TestReview_CountsTheSellerFrozenAtPurchase(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5,
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	h.catalog.missing = true

	edited, err := h.svc.UpdateReview(ctx, trustapi.UpdateReviewRequest{
		ActorID: buyer, ID: created.ID, Rating: new(int16(1)),
	})
	if err != nil {
		t.Fatalf("UpdateReview with the listing unreadable: %v", err)
	}
	if edited.Rating != 1 {
		t.Fatalf("review = %+v, want the edit applied", edited)
	}
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.ReviewRatingAverage != 1 || rep.ReviewRatingCount != 1 {
		t.Fatalf("review rating = %v over %d, want the edit on the right seller",
			rep.ReviewRatingAverage, rep.ReviewRatingCount)
	}
	// Nothing was filed against account 0 on the way.
	if junk := h.repo.reputation[key(0, domain.RoleSeller)]; junk.ReviewRatingCount != 0 {
		t.Fatalf("reputation of account 0 = %+v, want no such aggregate", junk)
	}

	if err := h.svc.DeleteReview(ctx, trustapi.ReviewRequest{ActorID: buyer, ID: created.ID}); err != nil {
		t.Fatalf("DeleteReview with the listing unreadable: %v", err)
	}
	rep, err = h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.ReviewRatingCount != 0 || rep.ReviewRatingAverage != 0 {
		t.Fatalf("review rating = %+v, want the rating taken back out", rep)
	}
}

// Evidence that names no confirmed upload is refused: a review photo that does not render is
// worse than a review with none.
func TestSubmitReview_RefusesUnknownAttachments(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4,
		Attachments: []id.ID[id.Resource]{id.Of[id.Resource](7)},
	}))); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
	h.images[7] = true
	got, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4,
		Attachments: []id.ID[id.Resource]{id.Of[id.Resource](7)},
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want the confirmed upload resolved", got.Attachments)
	}
}

// An edit moves the seller's aggregate by the difference, and is the author's alone.
func TestUpdateReview_MovesTheAggregate(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5, Body: "great",
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	// A moderator may remove a review, not rewrite one.
	if got := status(t, mustErr(h.svc.UpdateReview(ctx, trustapi.UpdateReviewRequest{
		ActorID: moderator, ID: created.ID, Rating: new(int16(1)),
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for somebody else's review", got)
	}
	edited, err := h.svc.UpdateReview(ctx, trustapi.UpdateReviewRequest{
		ActorID: buyer, ID: created.ID, Rating: new(int16(1)), Body: new("broke in a week"),
	})
	if err != nil {
		t.Fatalf("UpdateReview: %v", err)
	}
	if edited.Rating != 1 || edited.UpdatedAt == nil {
		t.Fatalf("review = %+v, want the edit recorded", edited)
	}
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	// A 5 rewritten to a 1 that left the average at 5 is a number nobody can reproduce.
	if rep.ReviewRatingAverage != 1 || rep.ReviewRatingCount != 1 {
		t.Fatalf("review rating = %v over %d, want the edit folded in", rep.ReviewRatingAverage, rep.ReviewRatingCount)
	}
	if synced := h.catalog.synced[listingID.Int64()]; synced != [2]float64{1, 1} {
		t.Errorf("synced = %v, want the cache to follow the edit", synced)
	}
}

// Removal takes the rating back out, and a moderator may do it on an upheld report.
func TestDeleteReview_TakesTheRatingBack(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5,
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got := status(t, mustErr(struct{}{}, h.svc.DeleteReview(ctx, trustapi.ReviewRequest{
		ActorID: stranger, ID: created.ID,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for a stranger", got)
	}
	if err := h.svc.DeleteReview(ctx, trustapi.ReviewRequest{ActorID: moderator, ID: created.ID}); err != nil {
		t.Fatalf("DeleteReview: %v", err)
	}
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.ReviewRatingCount != 0 || rep.ReviewRatingAverage != 0 {
		t.Fatalf("review rating = %v over %d, want it gone", rep.ReviewRatingAverage, rep.ReviewRatingCount)
	}
	if synced := h.catalog.synced[listingID.Int64()]; synced != [2]float64{0, 0} {
		t.Errorf("synced = %v, want the cache cleared", synced)
	}
}

// The thread: a reply is marked as the seller's, counted on the review, and capped on a page.
func TestReplies_MarkedAndCounted(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 3,
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	reply, err := h.svc.SubmitReply(ctx, trustapi.SubmitReplyRequest{
		ActorID: seller, ReviewID: created.ID, Body: "sorry to hear that",
	})
	if err != nil {
		t.Fatalf("SubmitReply: %v", err)
	}
	if !reply.IsSeller {
		t.Error("the listing's owner is not marked as the seller")
	}
	read, err := h.svc.GetReview(ctx, trustapi.GetReviewRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if read.ReplyCount != 1 || len(read.Replies) != 1 {
		t.Fatalf("review = %+v, want the thread counted and carried", read)
	}
	// Deleting is the author's, or a moderator's.
	if got := status(t, mustErr(struct{}{}, h.svc.DeleteReply(ctx, trustapi.ReplyRequest{
		ActorID: stranger, ID: reply.ID,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	if err := h.svc.DeleteReply(ctx, trustapi.ReplyRequest{ActorID: seller, ID: reply.ID}); err != nil {
		t.Fatalf("DeleteReply: %v", err)
	}
	read, err = h.svc.GetReview(ctx, trustapi.GetReviewRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if read.ReplyCount != 0 || len(read.Replies) != 0 {
		t.Fatalf("review = %+v, want the thread empty again", read)
	}
}

// One vote per account, replaced in place: flipping moves one unit between the two totals
// rather than adding a second.
func TestVoteReview_FlippedInPlace(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5,
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	// An author cannot inflate their own tally.
	if got := status(t, mustErr(h.svc.VoteReview(ctx, trustapi.VoteReviewRequest{
		ActorID: buyer, ID: created.ID, Vote: 1,
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 for a self vote", got)
	}
	tally, err := h.svc.VoteReview(ctx, trustapi.VoteReviewRequest{
		ActorID: stranger, ID: created.ID, Vote: 1,
	})
	if err != nil {
		t.Fatalf("VoteReview: %v", err)
	}
	if tally.Helpful != 1 || tally.NotHelpful != 0 {
		t.Fatalf("tally = %+v, want one helpful", tally)
	}
	tally, err = h.svc.VoteReview(ctx, trustapi.VoteReviewRequest{
		ActorID: stranger, ID: created.ID, Vote: -1,
	})
	if err != nil {
		t.Fatalf("VoteReview: %v", err)
	}
	if tally.Helpful != 0 || tally.NotHelpful != 1 {
		t.Fatalf("tally = %+v, want the vote moved rather than added", tally)
	}
	// The page carries the caller's own vote back, so the button they pressed renders.
	page, err := h.svc.ListReviews(ctx, trustapi.ListReviewsRequest{
		ListingID: listingID, ViewerID: stranger, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Votes.MyVote == nil || *page.Data[0].Votes.MyVote != -1 {
		t.Fatalf("page = %+v, want my own vote back", page.Data)
	}
	// Withdrawing removes the row rather than storing a neutral one.
	tally, err = h.svc.UnvoteReview(ctx, trustapi.ReviewRequest{ActorID: stranger, ID: created.ID})
	if err != nil {
		t.Fatalf("UnvoteReview: %v", err)
	}
	if tally.Helpful != 0 || tally.NotHelpful != 0 || tally.MyVote != nil {
		t.Fatalf("tally = %+v, want the vote gone", tally)
	}
	if got := status(t, mustErr(h.svc.UnvoteReview(ctx, trustapi.ReviewRequest{
		ActorID: stranger, ID: created.ID,
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 withdrawing a vote nobody cast", got)
	}
}

// seedReview writes a review straight into the fake with the tally and the age a paging test
// needs: the service will not produce a hundred votes or a six-year-old row on its own.
func seedReview(h *harness, helpful int64, createdAt time.Time) domain.Review {
	v := domain.Review{
		ID: h.repo.next(), ListingID: listingID.Int64(), OrderID: h.repo.next(),
		AuthorID: buyer.Int64(), SellerID: seller.Int64(), Rating: 5,
		HelpfulCount: helpful, CreatedAt: createdAt,
	}
	h.repo.reviews[v.ID] = v
	return v
}

func reviewIDs(page trustapi.ReviewPage) []id.ID[id.Review] {
	out := make([]id.ID[id.Review], 0, len(page.Data))
	for _, row := range page.Data {
		out = append(out, row.ID)
	}
	return out
}

// Each sort pages on the key it orders by. `helpful` bounded by a timestamp used to end page one
// at an old but much-upvoted review and then make every newer review unreachable, however many
// pages a client asked for.
func TestListReviews_HelpfulPagesOnItsOwnKey(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	popular := seedReview(h, 100, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	recent := seedReview(h, 90, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	first, err := h.svc.ListReviews(ctx, trustapi.ListReviewsRequest{
		ListingID: listingID, Sort: "helpful", Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if got := reviewIDs(first); len(got) != 1 || got[0] != id.Of[id.Review](popular.ID) {
		t.Fatalf("page 1 = %v, want the most helpful review", got)
	}
	if !first.Meta.HasMore || first.Meta.NextCursor == "" {
		t.Fatalf("meta = %+v, want another page", first.Meta)
	}
	second, err := h.svc.ListReviews(ctx, trustapi.ListReviewsRequest{
		ListingID: listingID, Sort: "helpful", Cursor: first.Meta.NextCursor, Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListReviews page 2: %v", err)
	}
	if got := reviewIDs(second); len(got) != 1 || got[0] != id.Of[id.Review](recent.ID) {
		t.Fatalf("page 2 = %v, want the 90-helpful review reachable", got)
	}
	if second.Meta.HasMore {
		t.Errorf("meta = %+v, want the traversal finished", second.Meta)
	}
}

// A cursor carries the key *and* the row id, so a tie at the boundary skips nothing.
// CURRENT_TIMESTAMP is transaction-scoped, so two rows written together share it exactly —
// which a bare timestamp cursor answered by dropping one of them.
func TestListReviews_CursorBreaksATieOnTheKey(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	first := seedReview(h, 0, at)
	second := seedReview(h, 0, at)

	var seen []id.ID[id.Review]
	cursor := ""
	for range 3 {
		page, err := h.svc.ListReviews(ctx, trustapi.ListReviewsRequest{
			ListingID: listingID, Cursor: cursor, Limit: 1,
		})
		if err != nil {
			t.Fatalf("ListReviews: %v", err)
		}
		seen = append(seen, reviewIDs(page)...)
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	want := []id.ID[id.Review]{id.Of[id.Review](second.ID), id.Of[id.Review](first.ID)}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("traversal = %v, want both rows that share a timestamp %v", seen, want)
	}
}

// A cursor this API did not issue is refused rather than read as "start again".
func TestListReviews_RefusesAForgedCursor(t *testing.T) {
	h := newHarness("completed")
	if got := status(t, mustErr(h.svc.ListReviews(context.Background(), trustapi.ListReviewsRequest{
		ListingID: listingID, Cursor: "not-a-cursor", Limit: 10,
	}))); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// The photo set is capped on the way in and on the way through: an edit is not a way around a
// limit the submission enforces.
func TestUpdateReview_CapsTheAttachmentSet(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	created, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4,
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	tooMany := make([]id.ID[id.Resource], 0, 11)
	for i := range 11 {
		key := int64(100 + i)
		h.images[key] = true
		tooMany = append(tooMany, id.Of[id.Resource](key))
	}
	invalidField(t, mustErr(h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: id.Of[id.Order](77), Rating: 4,
		Attachments: tooMany,
	})), "attachments")
	invalidField(t, mustErr(h.svc.UpdateReview(ctx, trustapi.UpdateReviewRequest{
		ActorID: buyer, ID: created.ID, Attachments: &tooMany,
	})), "attachments")
}

// A slot alone attaches to nothing: it has to be confirmed first, and confirming before the
// bytes arrive is refused rather than producing a review that renders a broken image. A
// resource id is guessable, so confirming somebody else's slot is refused too.
func TestUpload_ConfirmedBeforeItCanBeAttached(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()

	slot, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: buyer, Filename: "front.jpg", Mime: "image/jpeg", Size: 2048,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if slot.URL == "" || slot.ResourceID == 0 || !slot.ExpiresAt.After(time.Now()) {
		t.Fatalf("slot = %+v, want somewhere to PUT and a future expiry", slot)
	}

	// Unconfirmed, so it names no usable upload: attaching it to a review is refused exactly
	// as a made-up id would be.
	if got := status(t, mustErr(h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4,
		Attachments: []id.ID[id.Resource]{slot.ResourceID},
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 attaching an unconfirmed upload", got)
	}
	// And confirming before the bytes are there is refused too, rather than producing a row
	// that renders as a broken image.
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: buyer, ID: slot.ResourceID,
	})); err == nil {
		t.Fatal("an upload was confirmed before anything was uploaded")
	}

	// The client PUTs, then confirms.
	h.uploads.arrived[slot.ResourceID.Int64()] = true
	res, err := h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: buyer, ID: slot.ResourceID,
	})
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if res.ID != slot.ResourceID {
		t.Fatalf("confirmed = %+v, want the slot's own resource", res)
	}

	// Now it attaches, and the review renders it with a link rather than a bare id.
	review, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 4,
		Attachments: []id.ID[id.Resource]{slot.ResourceID},
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if len(review.Attachments) != 1 || review.Attachments[0].URL == "" {
		t.Fatalf("attachments = %+v, want one with a signed link on it", review.Attachments)
	}

	// Somebody else's slot is not theirs to confirm.
	other, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: buyer, Filename: "back.jpg", Mime: "image/jpeg", Size: 1024,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	h.uploads.arrived[other.ResourceID.Int64()] = true
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: stranger, ID: other.ResourceID,
	})); err == nil {
		t.Fatal("a stranger confirmed somebody else's upload")
	}
}

// invalidField asserts a request was refused by validation, naming the field that failed. The
// service answers with the validator's own result; the gateway is what renders it as the 400
// envelope, so there is no coded error to decompose here.
func invalidField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), field) {
		t.Fatalf("error = %v, want validation to name %q", err, field)
	}
}

// An anonymous reader gets the page with no vote of their own, which is what optionalAuth is
// for.
func TestListReviews_AnonymousHasNoVote(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	if _, err := h.svc.SubmitReview(ctx, trustapi.SubmitReviewRequest{
		ActorID: buyer, ListingID: listingID, OrderID: orderID, Rating: 5,
	}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	page, err := h.svc.ListReviews(ctx, trustapi.ListReviewsRequest{ListingID: listingID, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Votes.MyVote != nil {
		t.Fatalf("page = %+v, want a vote-less read", page.Data)
	}
}

// ----------------------------------------------------------------- tickets ---

// A report names a target that exists, one open one per reporter per target, and the id has to
// agree with the declared type.
func TestOpenTicket_ChecksTheTarget(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	req := trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportListing, Subject: "Hàng giả",
		RefID: listingID.String(), Reason: "counterfeit",
		Body: "same photos as the brand store",
	}
	got, err := h.svc.OpenTicket(ctx, req)
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	if got.RefID == nil || *got.RefID != listingID.String() || got.Status != domain.StatusOpen {
		t.Fatalf("ticket = %+v, want it open against the listing", got)
	}
	// The requester's own words are the thread's first message, so a ticket always has one to
	// answer in.
	if got.ConversationID == nil {
		t.Fatalf("ticket = %+v, want a conversation attached", got)
	}
	// One open ticket per target.
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// An id whose prefix disagrees with the kind is refused before anything is written.
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportAccount, Subject: "Kẻ lừa đảo",
		RefID: listingID.String(), Reason: "scam",
	}))); got != 400 {
		t.Fatalf("status = %d, want 400 for a mismatched prefix", got)
	}
	// And a target that does not exist cannot fill the queue.
	h.catalog.missing = true
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: seller, Kind: domain.KindReportListing, Subject: "Tin giả",
		RefID: listingID.String(), Reason: "scam",
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for a target that is gone", got)
	}
}

// A module that cannot be reached has not said the target is gone. Telling a reporter their
// target does not exist because catalog was down for a second makes them stop reporting it.
func TestOpenTicket_PropagatesAnOutage(t *testing.T) {
	h := newHarness("completed")
	h.catalog.getErr = errx.NewError(503, "catalog_unavailable", "catalog is unreachable")
	err := mustErr(h.svc.OpenTicket(context.Background(), trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportListing, Subject: "Lừa đảo",
		RefID: listingID.String(), Reason: "scam",
	}))
	if got := status(t, err); got != 503 {
		t.Fatalf("status = %d, want the outage propagated", got)
	}
	if errors.Is(err, domain.ErrTicketTargetMissing) {
		t.Error("an unreachable module was reported as a target that does not exist")
	}
}

// What the requester wrote is the thread's first message, not a column here — so a support request
// needs no body of its own and no second upload path. A thread that could not be opened is repaired
// on the next read, because the row is the ticket and losing the conversation must not lose it.
func TestOpenTicket_TheRequestersWordsAreTheThread(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	photo := id.ID[id.Resource](77)
	filed, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindFeatureRequest, Subject: "Cho phép lọc theo tỉnh",
		Body: "tôi muốn tìm hàng gần nhà", Attachments: []id.ID[id.Resource]{photo},
	})
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	if len(h.chat.opened) != 1 || h.chat.opened[0].Body != "tôi muốn tìm hàng gần nhà" ||
		len(h.chat.opened[0].Attachments) != 1 || h.chat.opened[0].Attachments[0] != photo {
		t.Fatalf("opened = %+v, want the body and the photo handed to chat", h.chat.opened)
	}
	// A feature request is about nothing, so it carries no target and no reason.
	if filed.RefID != nil || filed.Reason != nil {
		t.Fatalf("ticket = %+v, want no target and no reason on a feature request", filed)
	}

	// Chat unreachable: the ticket still exists, mute.
	h.chat.openErr = errx.NewError(503, "chat_unavailable", "chat is unreachable")
	mute, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: seller, Kind: domain.KindPayment, Subject: "Chưa nhận được tiền",
	})
	if err != nil {
		t.Fatalf("OpenTicket while chat was down: %v", err)
	}
	if mute.ConversationID != nil {
		t.Fatalf("ticket = %+v, want no thread while chat was down", mute)
	}
	// And the read repairs it, so staff have somewhere to answer.
	h.chat.openErr = nil
	repaired, err := h.svc.GetTicket(ctx, trustapi.TicketRequest{ActorID: seller, ID: mute.ID})
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if repaired.ConversationID == nil {
		t.Fatalf("ticket = %+v, want the thread repaired on read", repaired)
	}
}

// A refund dispute is order's status first and a ticket second: staff cannot be asked to decide a
// refund that order says is not theirs to decide, and a ticket about one would be a queue entry with
// no possible answer.
func TestOpenTicket_RefundDisputeEscalatesInOrderFirst(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	refundID := id.ID[id.Refund](55)
	// A refund-dispute ticket names the *order*, not the case: one live refund per order is an
	// index, so order resolves which row that is — and every dispute about one sale then files
	// against the same target, which is what puts a seller's later complaint in the same queue.
	req := trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindRefundDispute, Subject: "Hàng không đúng mô tả",
		RefID: orderID.String(), Body: "ảnh mở hộp đây",
	}

	// Order refuses: nothing is written here either.
	h.orders.escalateErr = errx.NewError(409, "refund_not_escalatable", "refund cannot be escalated")
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want order's refusal propagated", got)
	}
	if len(h.repo.tickets) != 0 {
		t.Fatalf("tickets = %+v, want none for a refund order would not escalate", h.repo.tickets)
	}

	h.orders.escalateErr = nil
	filed, err := h.svc.OpenTicket(ctx, req)
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	if len(h.orders.escalated) != 1 || h.orders.escalated[0] != orderID {
		t.Fatalf("escalated = %v, want the sale handed to order", h.orders.escalated)
	}
	if filed.RefType == nil || *filed.RefType != domain.RefOrder || filed.ConversationID == nil {
		t.Fatalf("ticket = %+v, want it about the order with a thread", filed)
	}

	// Staff cannot close it by hand: the verdict is the money moving, and a ticket marked settled
	// with the escrow untouched is the one outcome nobody can undo from here.
	if _, err := h.svc.AdminClaimTicket(ctx, trustapi.TicketRequest{ActorID: moderator, ID: filed.ID}); err != nil {
		t.Fatalf("AdminClaimTicket: %v", err)
	}
	if got := status(t, mustErr(h.svc.AdminResolveTicket(ctx, trustapi.ResolveTicketRequest{
		ActorID: moderator, ID: filed.ID, ActionTaken: domain.ActionNone,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 for a verdict that belongs to order", got)
	}

	// Order's verdict is what closes it, and the requester reads it in the thread. Delivered twice
	// counts once: the bus is at-least-once and a second Resolve would argue with a recorded one.
	verdict := trustapi.RecordRefundVerdictRequest{
		OrderID: orderID, RefundID: refundID, ModeratorID: moderator,
		BuyerWins: true, Note: "ảnh mở hộp rõ ràng",
	}
	for range 2 {
		if err := h.svc.RecordRefundVerdict(ctx, verdict); err != nil {
			t.Fatalf("RecordRefundVerdict: %v", err)
		}
	}
	closed, err := h.svc.GetTicket(ctx, trustapi.TicketRequest{ActorID: buyer, ID: filed.ID})
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if closed.Status != domain.StatusResolved || closed.ActionTaken == nil ||
		*closed.ActionTaken != domain.ActionRefundGranted {
		t.Fatalf("ticket = %+v, want it closed as granted", closed)
	}
	if len(h.chat.posted) != 1 || h.chat.posted[0].Body != verdict.Note {
		t.Fatalf("posted = %+v, want the verdict in the thread once", h.chat.posted)
	}

	// A verdict on a sale nobody raised a ticket about is nothing to do, not a redelivery loop.
	if err := h.svc.RecordRefundVerdict(ctx, trustapi.RecordRefundVerdictRequest{
		OrderID: id.ID[id.Order](999), RefundID: id.ID[id.Refund](999), ModeratorID: moderator,
	}); err != nil {
		t.Fatalf("RecordRefundVerdict for an unknown refund: %v", err)
	}
}

// Both parties may escalate one sale and the index holds one open ticket per *requester* per
// target, so two tickets about one order is a legal state. Order publishes one verdict, so it has
// to close all of them: a refund dispute cannot be resolved by hand, which makes a ticket left open
// on a decided refund a queue entry with no possible answer.
func TestRecordRefundVerdict_ClosesEveryTicketAboutTheRefund(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	refundID := id.ID[id.Refund](55)
	requesters := []id.ID[id.Account]{buyer, seller}
	filed := make([]id.ID[id.Ticket], 0, len(requesters))
	for _, requester := range requesters {
		one, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
			ActorID: requester, Kind: domain.KindRefundDispute, Subject: "Hàng không đúng mô tả",
			RefID: orderID.String(),
		})
		if err != nil {
			t.Fatalf("OpenTicket by %v: %v", requester, err)
		}
		filed = append(filed, one.ID)
	}
	if err := h.svc.RecordRefundVerdict(ctx, trustapi.RecordRefundVerdictRequest{
		OrderID: orderID, RefundID: refundID, ModeratorID: moderator, Note: "hàng về đúng mô tả",
	}); err != nil {
		t.Fatalf("RecordRefundVerdict: %v", err)
	}
	for i, ticketID := range filed {
		got, err := h.svc.GetTicket(ctx, trustapi.TicketRequest{ActorID: requesters[i], ID: ticketID})
		if err != nil {
			t.Fatalf("GetTicket: %v", err)
		}
		if got.Status != domain.StatusResolved || got.ActionTaken == nil ||
			*got.ActionTaken != domain.ActionRefundRefused {
			t.Fatalf("ticket = %+v, want the verdict recorded on every ticket about the refund", got)
		}
	}
	// Each requester is answered in their own thread, since each raised their own case.
	if len(h.chat.posted) != len(filed) {
		t.Fatalf("posted = %+v, want the verdict in every thread", h.chat.posted)
	}
}

// A ticket about nothing in particular — a feature request, a payment question — stores no target at
// all, and the moderator read has to answer that rather than dereference it.
func TestAdminGetTicket_ATicketAboutNothing(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	filed, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindFeatureRequest, Subject: "Cho phép lọc theo tỉnh",
	})
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	entry, err := h.svc.AdminGetTicket(ctx, trustapi.TicketRequest{ActorID: moderator, ID: filed.ID})
	if err != nil {
		t.Fatalf("AdminGetTicket: %v", err)
	}
	if entry.Target != nil || entry.OpenTicketsAgainstTarget != 0 {
		t.Fatalf("entry = %+v, want no target on a ticket about nothing", entry)
	}
	if _, err := h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{
		ActorID: moderator, Limit: 10,
	}); err != nil {
		t.Fatalf("AdminListTickets: %v", err)
	}
}

// A reason is a report's grounds, in both directions: a report with none is refused, and one on a
// payment question is refused rather than dropped — a 201 whose ticket lost a field the client sent
// says the platform recorded something it did not.
func TestOpenTicket_AReasonBelongsToAReport(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindPayment, Subject: "Chưa nhận được tiền", Reason: "scam",
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 for a reason on a kind that has none", got)
	}
	if len(h.repo.tickets) != 0 {
		t.Fatalf("tickets = %+v, want none written", h.repo.tickets)
	}
	if got := status(t, mustErr(h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportListing, Subject: "Hàng giả",
		RefID: listingID.String(),
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 for a report with no reason", got)
	}
}

// A write that simply failed is not "somebody else claimed it": answering 409 to a dropped
// connection sends a moderator looking for a colleague who was never there.
func TestAdminClaimTicket_PropagatesARealFailure(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	filed, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportListing, Subject: "Spam",
		RefID: listingID.String(), Reason: "spam",
	})
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	h.repo.saveTicketErr = errors.New("connection refused")
	claimErr := mustErr(h.svc.AdminClaimTicket(ctx, trustapi.TicketRequest{
		ActorID: moderator, ID: filed.ID,
	}))
	if errors.Is(claimErr, domain.ErrTicketNotClaimable) {
		t.Fatalf("claim error = %v, want the real failure rather than a conflict", claimErr)
	}
	if !errors.Is(claimErr, h.repo.saveTicketErr) {
		t.Fatalf("claim error = %v, want it to carry the write failure", claimErr)
	}
}

// The queue is a page, so it costs a page's worth of round trips: one count for every target on
// it and one name per distinct account, not three lookups per row.
func TestAdminListTickets_BatchesThePage(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	for _, reporter := range []id.ID[id.Account]{buyer, seller, stranger} {
		if _, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
			ActorID: reporter, Kind: domain.KindReportListing, Subject: "Hàng giả",
			RefID: listingID.String(), Reason: "counterfeit",
		}); err != nil {
			t.Fatalf("OpenTicket by %v: %v", reporter, err)
		}
	}
	h.repo.countCalls = 0
	queue, err := h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{
		ActorID: moderator, Limit: 10,
	})
	if err != nil {
		t.Fatalf("AdminListTickets: %v", err)
	}
	if len(queue.Data) != 3 {
		t.Fatalf("queue = %d entries, want the three open reports", len(queue.Data))
	}
	if h.repo.countCalls != 1 {
		t.Errorf("pattern reads = %d, want one for the whole page", h.repo.countCalls)
	}
	for _, entry := range queue.Data {
		if entry.OpenTicketsAgainstTarget != 3 {
			t.Errorf("entry = %+v, want all three reports counted against the target", entry)
		}
	}
}

// The queue is worked oldest first and a reporter's history reads newest first, so the cursor
// runs in both directions — and neither may drop a row whose timestamp its neighbour shares.
func TestTickets_PageInBothDirections(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	var filed []id.ID[id.Ticket]
	for i, target := range []int64{41, 42, 43} {
		r, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
			ActorID: buyer, Kind: domain.KindReportListing, Subject: "Spam",
			RefID: id.Of[id.Listing](target).String(), Reason: "spam",
		})
		if err != nil {
			t.Fatalf("OpenTicket %d: %v", i, err)
		}
		// One transaction's rows share created_at exactly, which is what the tuple cursor is for.
		stored := h.repo.tickets[r.ID.Int64()]
		stored.CreatedAt = at
		h.repo.tickets[r.ID.Int64()] = stored
		filed = append(filed, r.ID)
	}

	// The reporter's own history, newest first.
	var mine []id.ID[id.Ticket]
	cursor := ""
	for range 4 {
		page, err := h.svc.ListMyTickets(ctx, trustapi.ListTicketsRequest{
			ActorID: buyer, Cursor: cursor, Limit: 1,
		})
		if err != nil {
			t.Fatalf("ListMyTickets: %v", err)
		}
		for _, row := range page.Data {
			mine = append(mine, row.ID)
		}
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	if len(mine) != 3 {
		t.Fatalf("history = %v, want all three rows across the pages", mine)
	}
	if mine[0] != filed[2] || mine[2] != filed[0] {
		t.Errorf("history = %v, want newest first out of %v", mine, filed)
	}

	// The moderator queue, oldest first.
	var queue []id.ID[id.Ticket]
	cursor = ""
	for range 4 {
		page, err := h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{
			ActorID: moderator, Cursor: cursor, Limit: 1,
		})
		if err != nil {
			t.Fatalf("AdminListTickets: %v", err)
		}
		for _, entry := range page.Data {
			queue = append(queue, entry.Ticket.ID)
		}
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	if len(queue) != 3 {
		t.Fatalf("queue = %v, want all three rows across the pages", queue)
	}
	if queue[0] != filed[0] || queue[2] != filed[2] {
		t.Errorf("queue = %v, want oldest first out of %v", queue, filed)
	}
}

// The order counters are a mirror of an at-least-once stream, so the same settlement arriving
// twice counts once — that is what makes the contract's "idempotent" true.
func TestRecordOrderOutcome_CountsAnOrderOnce(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	req := trustapi.RecordOrderOutcomeRequest{
		OrderID: orderID, BuyerID: buyer, SellerID: seller, Completed: true,
	}
	for range 3 {
		if err := h.svc.RecordOrderOutcome(ctx, req); err != nil {
			t.Fatalf("RecordOrderOutcome: %v", err)
		}
	}
	rep, err := h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.CompletedOrders != 1 {
		t.Fatalf("completed orders = %d, want the redeliveries ignored", rep.CompletedOrders)
	}
	// A different order is a different fact.
	second := req
	second.OrderID = id.Of[id.Order](11)
	second.Completed = false
	if err := h.svc.RecordOrderOutcome(ctx, second); err != nil {
		t.Fatalf("RecordOrderOutcome: %v", err)
	}
	rep, err = h.svc.GetReputation(ctx, trustapi.GetReputationRequest{AccountID: seller, Role: "seller"})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if rep.CompletedOrders != 1 || rep.CancelledOrders != 1 {
		t.Fatalf("counters = %d/%d, want one of each", rep.CompletedOrders, rep.CancelledOrders)
	}
}

// The moderator surface: the queue is staff-only, claiming is once, and a verdict names what
// was done about it.
func TestTicketQueue_ClaimThenResolve(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	filed, err := h.svc.OpenTicket(ctx, trustapi.OpenTicketRequest{
		ActorID: buyer, Kind: domain.KindReportListing, Subject: "Hàng giả",
		RefID: listingID.String(), Reason: "counterfeit",
	})
	if err != nil {
		t.Fatalf("OpenTicket: %v", err)
	}
	if got := status(t, mustErr(h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{
		ActorID: buyer, Limit: 10,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for an ordinary caller", got)
	}
	queue, err := h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{ActorID: moderator, Limit: 10})
	if err != nil {
		t.Fatalf("AdminListTickets: %v", err)
	}
	if len(queue.Data) != 1 || queue.Data[0].OpenTicketsAgainstTarget != 1 {
		t.Fatalf("queue = %+v, want the open ticket and its pattern", queue.Data)
	}
	// The single read carries the reported content beside the ticket.
	entry, err := h.svc.AdminGetTicket(ctx, trustapi.TicketRequest{ActorID: moderator, ID: filed.ID})
	if err != nil {
		t.Fatalf("AdminGetTicket: %v", err)
	}
	if entry.Target == nil || entry.Requester.ID != buyer {
		t.Fatalf("entry = %+v, want the target and the requester", entry)
	}

	claimed, err := h.svc.AdminClaimTicket(ctx, trustapi.TicketRequest{ActorID: moderator, ID: filed.ID})
	if err != nil {
		t.Fatalf("AdminClaimTicket: %v", err)
	}
	if claimed.Status != domain.StatusReviewing {
		t.Fatalf("status = %q, want reviewing", claimed.Status)
	}
	// Claiming twice loses, so two moderators do not work the same case.
	if got := status(t, mustErr(h.svc.AdminClaimTicket(ctx, trustapi.TicketRequest{
		ActorID: moderator, ID: filed.ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}

	// The contract itself is what refuses an action this route does not have: one nobody defined, and
	// the two refund-* values, which only order's verdict writes. Both are a 400 on the field rather
	// than free text a report reader has to interpret.
	for _, action := range []string{"listing-deleted-maybe", domain.ActionRefundGranted} {
		if got := status(t, mustErr(h.svc.AdminResolveTicket(ctx, trustapi.ResolveTicketRequest{
			ActorID: moderator, ID: filed.ID, ActionTaken: action,
		}))); got != 400 {
			t.Fatalf("status = %d for %q, want 400", got, action)
		}
	}
	resolved, err := h.svc.AdminResolveTicket(ctx, trustapi.ResolveTicketRequest{
		ActorID: moderator, ID: filed.ID, ActionTaken: domain.ActionListingRemoved,
		Note: "counterfeit confirmed",
	})
	if err != nil {
		t.Fatalf("AdminResolveTicket: %v", err)
	}
	if resolved.Status != domain.StatusResolved || resolved.ActionTaken == nil ||
		*resolved.ActionTaken != domain.ActionListingRemoved || resolved.ResolvedAt == nil {
		t.Fatalf("ticket = %+v, want a recorded verdict", resolved)
	}
	// Resolved once: a second verdict argues against a decision already recorded.
	if got := status(t, mustErr(h.svc.AdminResolveTicket(ctx, trustapi.ResolveTicketRequest{
		ActorID: moderator, ID: filed.ID, ActionTaken: domain.ActionNone,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// And it leaves the queue, so nobody works it twice.
	queue, err = h.svc.AdminListTickets(ctx, trustapi.AdminListTicketsRequest{ActorID: moderator, Limit: 10})
	if err != nil {
		t.Fatalf("AdminListTickets: %v", err)
	}
	if len(queue.Data) != 0 {
		t.Fatalf("queue = %+v, want the resolved case gone", queue.Data)
	}
	// The reporter still sees their own history, whatever its status.
	mine, err := h.svc.ListMyTickets(ctx, trustapi.ListTicketsRequest{ActorID: buyer, Limit: 10})
	if err != nil {
		t.Fatalf("ListMyTickets: %v", err)
	}
	if len(mine.Data) != 1 || mine.Data[0].Status != domain.StatusResolved {
		t.Fatalf("my tickets = %+v, want the resolved one", mine.Data)
	}
}

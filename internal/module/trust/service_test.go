package trust_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/api/catalogtest"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/api/chattest"
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
	// missing hides the listing, which is what a report against a deleted one hits.
	missing bool
}

func (f *fakeCatalog) GetListing(_ context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
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
}

func (f fakeOrders) GetOrder(_ context.Context, req orderapi.OrderRequest) (orderapi.Order, error) {
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
}

func (f fakeChat) GetMessage(_ context.Context, req chatapi.GetMessageRequest) (chatapi.Message, error) {
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
}

func newHarness(state string) *harness {
	repo := newFakeRepo()
	catalog := &fakeCatalog{synced: map[int64][2]float64{}}
	orders := &fakeOrders{state: state}
	chat := &fakeChat{}
	accounts := fakeAccounts{
		roles:   map[id.ID[id.Account]]string{moderator: "moderator"},
		missing: map[id.ID[id.Account]]bool{},
	}
	svc := trust.NewService(repo, accounts, catalog, orders, chat,
		validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo, catalog: catalog, orders: orders, chat: chat, accounts: accounts}
}

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
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
		BuyerID: buyer, SellerID: seller, Completed: true,
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
	h.repo.resources[7] = true
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

// ----------------------------------------------------------------- reports ---

// A report names a target that exists, one open one per reporter per target, and the id has to
// agree with the declared type.
func TestSubmitReport_ChecksTheTarget(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	req := trustapi.SubmitReportRequest{
		ActorID: buyer, RefType: "listing", RefID: listingID.String(), Reason: "counterfeit",
		Detail: "same photos as the brand store",
	}
	got, err := h.svc.SubmitReport(ctx, req)
	if err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if got.RefID != listingID.String() || got.Status != domain.ReportStatusOpen {
		t.Fatalf("report = %+v, want it open against the listing", got)
	}
	// One open report per target.
	if got := status(t, mustErr(h.svc.SubmitReport(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// An id whose prefix disagrees with the type is refused before anything is written.
	if got := status(t, mustErr(h.svc.SubmitReport(ctx, trustapi.SubmitReportRequest{
		ActorID: buyer, RefType: "account", RefID: listingID.String(), Reason: "scam",
	}))); got != 400 {
		t.Fatalf("status = %d, want 400 for a mismatched prefix", got)
	}
	// And a target that does not exist cannot fill the queue.
	h.catalog.missing = true
	if got := status(t, mustErr(h.svc.SubmitReport(ctx, trustapi.SubmitReportRequest{
		ActorID: seller, RefType: "listing", RefID: listingID.String(), Reason: "scam",
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for a target that is gone", got)
	}
}

// The moderator surface: the queue is staff-only, claiming is once, and a verdict names what
// was done about it.
func TestReportQueue_ClaimThenResolve(t *testing.T) {
	h := newHarness("completed")
	ctx := context.Background()
	filed, err := h.svc.SubmitReport(ctx, trustapi.SubmitReportRequest{
		ActorID: buyer, RefType: "listing", RefID: listingID.String(), Reason: "counterfeit",
	})
	if err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if got := status(t, mustErr(h.svc.AdminListReports(ctx, trustapi.AdminListReportsRequest{
		ActorID: buyer, Limit: 10,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for an ordinary caller", got)
	}
	queue, err := h.svc.AdminListReports(ctx, trustapi.AdminListReportsRequest{ActorID: moderator, Limit: 10})
	if err != nil {
		t.Fatalf("AdminListReports: %v", err)
	}
	if len(queue.Data) != 1 || queue.Data[0].OpenReportsAgainstTarget != 1 {
		t.Fatalf("queue = %+v, want the open report and its pattern", queue.Data)
	}
	// The single read carries the reported content beside the report.
	entry, err := h.svc.AdminGetReport(ctx, trustapi.ReportRequest{ActorID: moderator, ID: filed.ID})
	if err != nil {
		t.Fatalf("AdminGetReport: %v", err)
	}
	if entry.Target == nil || entry.Reporter.ID != buyer {
		t.Fatalf("entry = %+v, want the target and the reporter", entry)
	}

	claimed, err := h.svc.AdminClaimReport(ctx, trustapi.ReportRequest{ActorID: moderator, ID: filed.ID})
	if err != nil {
		t.Fatalf("AdminClaimReport: %v", err)
	}
	if claimed.Status != domain.ReportStatusReviewing {
		t.Fatalf("status = %q, want reviewing", claimed.Status)
	}
	// Claiming twice loses, so two moderators do not work the same case.
	if got := status(t, mustErr(h.svc.AdminClaimReport(ctx, trustapi.ReportRequest{
		ActorID: moderator, ID: filed.ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}

	// A dismissal takes the action `none`; upholding one names what was done.
	if got := status(t, mustErr(h.svc.AdminResolveReport(ctx, trustapi.ResolveReportRequest{
		ActorID: moderator, ID: filed.ID, Status: "dismissed", ActionTaken: "listing-removed",
	}))); got != 400 {
		t.Fatalf("status = %d, want 400 for a dismissal that did something", got)
	}
	resolved, err := h.svc.AdminResolveReport(ctx, trustapi.ResolveReportRequest{
		ActorID: moderator, ID: filed.ID, Status: "actioned", ActionTaken: "listing-removed",
		Note: "counterfeit confirmed",
	})
	if err != nil {
		t.Fatalf("AdminResolveReport: %v", err)
	}
	if resolved.Status != domain.ReportStatusActioned || resolved.ActionTaken == nil ||
		*resolved.ActionTaken != "listing-removed" || resolved.ResolvedAt == nil {
		t.Fatalf("report = %+v, want a recorded verdict", resolved)
	}
	// Resolved once: a second verdict argues against a decision already recorded.
	if got := status(t, mustErr(h.svc.AdminResolveReport(ctx, trustapi.ResolveReportRequest{
		ActorID: moderator, ID: filed.ID, Status: "dismissed", ActionTaken: "none",
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// And it leaves the queue, so nobody works it twice.
	queue, err = h.svc.AdminListReports(ctx, trustapi.AdminListReportsRequest{ActorID: moderator, Limit: 10})
	if err != nil {
		t.Fatalf("AdminListReports: %v", err)
	}
	if len(queue.Data) != 0 {
		t.Fatalf("queue = %+v, want the resolved case gone", queue.Data)
	}
	// The reporter still sees their own history, whatever its status.
	mine, err := h.svc.ListMyReports(ctx, trustapi.ListReportsRequest{ActorID: buyer, Limit: 10})
	if err != nil {
		t.Fatalf("ListMyReports: %v", err)
	}
	if len(mine.Data) != 1 || mine.Data[0].Status != domain.ReportStatusActioned {
		t.Fatalf("my reports = %+v, want the resolved one", mine.Data)
	}
}

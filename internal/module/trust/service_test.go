package trust_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/module/trust"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

var (
	orderID = id.Of[id.Order](11)
	raterID = id.Of[id.Account](22)
	rateeID = id.Of[id.Account](33)
)

type fakeRepo struct {
	feedback   *domain.Feedback
	reputation domain.Reputation
	insertErr  error
}

func (f *fakeRepo) InsertFeedback(_ context.Context, fb *domain.Feedback) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	fb.ID = 1
	f.feedback = fb
	return nil
}

func (f *fakeRepo) FindReputation(_ context.Context, _ int64, _ string) (domain.Reputation, error) {
	return f.reputation, nil
}

func (f *fakeRepo) InsertReport(_ context.Context, r *domain.Report) error {
	r.ID = 2
	return nil
}

func TestSubmitFeedback(t *testing.T) {
	repo := &fakeRepo{}
	svc := trust.NewService(repo, slog.Default())
	got, err := svc.SubmitFeedback(context.Background(), trustapi.SubmitFeedbackRequest{
		RaterID: raterID, OrderID: orderID, RateeID: rateeID,
		Direction: domain.DirectionBuyerToSeller, Rating: 5, Comment: "great",
	})
	if err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	if got.ID != id.Of[id.Feedback](1) || got.Rating != 5 {
		t.Fatalf("unexpected feedback: %+v", got)
	}
	// Blind until published: a fresh rating must not be visible.
	if got.Published {
		t.Error("new feedback should stay blind (published=false)")
	}
}

func TestSubmitFeedback_RejectsSelfAndOutOfRange(t *testing.T) {
	svc := trust.NewService(&fakeRepo{}, slog.Default())
	cases := map[string]trustapi.SubmitFeedbackRequest{
		"self rating":   {RaterID: raterID, OrderID: orderID, RateeID: raterID, Direction: domain.DirectionBuyerToSeller, Rating: 5},
		"rating 6":      {RaterID: raterID, OrderID: orderID, RateeID: rateeID, Direction: domain.DirectionBuyerToSeller, Rating: 6},
		"bad direction": {RaterID: raterID, OrderID: orderID, RateeID: rateeID, Direction: "sideways", Rating: 3},
	}
	for name, req := range cases {
		if _, err := svc.SubmitFeedback(context.Background(), req); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
			t.Errorf("%s: expected 400, got %v", name, err)
		}
	}
}

func TestSubmitFeedback_DuplicatePropagatesConflict(t *testing.T) {
	repo := &fakeRepo{insertErr: domain.ErrFeedbackExists}
	svc := trust.NewService(repo, slog.Default())
	_, err := svc.SubmitFeedback(context.Background(), trustapi.SubmitFeedbackRequest{
		RaterID: raterID, OrderID: orderID, RateeID: rateeID, Direction: domain.DirectionBuyerToSeller, Rating: 4,
	})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 409 {
		t.Fatalf("expected 409 through the wrap, got %v", err)
	}
}

func TestGetReputation_ComputesAverage(t *testing.T) {
	repo := &fakeRepo{reputation: domain.Reputation{
		AccountID: rateeID.Int64(), Role: domain.RoleSeller, RatingSum: 9, RatingCount: 2, CompletedOrders: 2,
	}}
	svc := trust.NewService(repo, slog.Default())
	got, err := svc.GetReputation(context.Background(), trustapi.GetReputationRequest{AccountID: rateeID, Role: domain.RoleSeller})
	if err != nil {
		t.Fatalf("GetReputation: %v", err)
	}
	if got.AverageRating != 4.5 {
		t.Fatalf("average = %v, want 4.5", got.AverageRating)
	}
}

func TestReputation_AverageWithoutRatings(t *testing.T) {
	if got := (domain.Reputation{}).AverageRating(); got != 0 {
		t.Fatalf("average = %v, want 0 when nobody rated", got)
	}
}

func TestSubmitReport(t *testing.T) {
	svc := trust.NewService(&fakeRepo{}, slog.Default())
	got, err := svc.SubmitReport(context.Background(), trustapi.SubmitReportRequest{
		ReporterID: raterID, RefType: domain.ReportRefListing, RefID: id.Of[id.ProductSPU](5).String(), Reason: "scam",
	})
	if err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if got.ID != id.Of[id.Report](2) || got.Status != domain.ReportStatusOpen {
		t.Fatalf("unexpected report: %+v", got)
	}
}

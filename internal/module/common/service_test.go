package common_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/module/common"
	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/module/common/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	inserted *domain.Resource
	options  []domain.Option
}

func (f *fakeRepo) InsertResource(_ context.Context, r *domain.Resource) error {
	r.ID = 1
	f.inserted = r
	return nil
}

func (f *fakeRepo) ListEnabledOptions(_ context.Context, _ string) ([]domain.Option, error) {
	return f.options, nil
}

func TestRegisterResource(t *testing.T) {
	repo := &fakeRepo{}
	svc := common.NewService(repo, slog.Default())
	got, err := svc.RegisterResource(context.Background(), commonapi.RegisterResourceRequest{
		UploadedByID: id.Of[id.Account](1), Provider: "minio", ObjectKey: "a/b.jpg", Mime: "image/jpeg", Size: 12,
	})
	if err != nil {
		t.Fatalf("RegisterResource: %v", err)
	}
	if got.ID != id.Of[id.Resource](1) || got.Provider != "minio" {
		t.Fatalf("unexpected resource: %+v", got)
	}
	// Metadata defaults to an empty JSON object so the NOT NULL jsonb column holds valid JSON.
	if string(repo.inserted.Metadata) != "{}" {
		t.Errorf("metadata = %q, want {}", repo.inserted.Metadata)
	}
}

func TestRegisterResource_MissingProvider(t *testing.T) {
	svc := common.NewService(&fakeRepo{}, slog.Default())
	_, err := svc.RegisterResource(context.Background(), commonapi.RegisterResourceRequest{ObjectKey: "a", Mime: "image/png"})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestListOptions(t *testing.T) {
	repo := &fakeRepo{options: []domain.Option{
		{ID: "vnpay-1", Name: "VNPay", Type: domain.OptionTypePayment, Provider: "vnpay", Priority: 10, IsEnabled: true},
	}}
	svc := common.NewService(repo, slog.Default())
	got, err := svc.ListOptions(context.Background(), commonapi.ListOptionsRequest{Type: domain.OptionTypePayment})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "vnpay-1" || !got[0].IsEnabled {
		t.Fatalf("unexpected options: %+v", got)
	}
}

package account_test

import (
	"errors"
	"testing"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
)

func TestCreateNotificationWritesInApp(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	got, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryOrder),
		Title:     "Your order shipped",
		Payload:   map[string]any{"order_id": "ord_x"},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if got.Title != "Your order shipped" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(repo.notifs) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(repo.notifs))
	}
	if repo.notifs[0].AccountID != 42 {
		t.Errorf("AccountID = %d, want 42", repo.notifs[0].AccountID)
	}
}

// The in-app channel is a preference like any other: turning it off means no row,
// not a hidden row, because the feed has no notion of invisible entries.
func TestCreateNotificationRespectsPreference(t *testing.T) {
	repo := newFakeRepo()
	repo.prefs[42] = []domain.Preference{{
		AccountID: 42,
		Category:  domain.CategoryPromotion,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryPromotion),
		Title:     "50% off",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("wrote %d rows, want 0 — in-app is disabled", len(repo.notifs))
	}
}

func TestCreateNotificationRejectsUnknownCategory(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  "gossip",
		Title:     "t",
	})
	if !errors.Is(err, domain.ErrNotificationInvalid) {
		t.Fatalf("err = %v, want ErrNotificationInvalid", err)
	}
	if len(repo.notifs) != 0 {
		t.Errorf("wrote %d rows, want 0", len(repo.notifs))
	}
}

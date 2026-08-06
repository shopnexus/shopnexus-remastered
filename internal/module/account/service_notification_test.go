package account_test

import (
	"encoding/json/v2"
	"errors"
	"sync"
	"testing"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/realtime"
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

// recorder captures what the service pushed, so a test asserts on recipients and codes
// without a bus. Copied from chat's service_realtime_test.go: a test double shared
// across packages via an exported helper is worse than a few duplicated lines.
type recorder struct {
	mu   sync.Mutex
	sent []recorded
}

type recorded struct {
	subject string
	env     realtime.Envelope
}

func (r *recorder) Broadcast(subject string, b []byte) error {
	var env realtime.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, recorded{subject: subject, env: env})
	return nil
}

func (r *recorder) OnBroadcast(string, func([]byte)) (func(), error) { return func() {}, nil }

func TestCreateNotificationPushesToTheOwner(t *testing.T) {
	repo := newFakeRepo()
	rec := &recorder{}
	svc := newTestServiceWithFanout(t, repo, rec)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryOrder),
		Title:     "Your order shipped",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	if len(rec.sent) != 1 {
		t.Fatalf("pushed %d events, want 1", len(rec.sent))
	}
	if got, want := rec.sent[0].subject, realtime.AccountSubject(42); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
	if rec.sent[0].env.Code != "account.notification_created" {
		t.Errorf("code = %q", rec.sent[0].env.Code)
	}
}

// No row means no event: a disabled in-app preference must not push a notification
// the feed will never show.
func TestCreateNotificationDoesNotPushWhenSuppressed(t *testing.T) {
	repo := newFakeRepo()
	repo.prefs[42] = []domain.Preference{{
		AccountID: 42,
		Category:  domain.CategoryPromotion,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}
	rec := &recorder{}
	svc := newTestServiceWithFanout(t, repo, rec)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryPromotion),
		Title:     "50% off",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(rec.sent) != 0 {
		t.Fatalf("pushed %d events, want 0", len(rec.sent))
	}
}

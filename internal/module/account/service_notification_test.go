package account_test

import (
	"encoding/json/v2"
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
		Kind:      string(domain.KindOrderDelivered),
		Payload:   map[string]any{"order_id": "ord_x"},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	// The answer is written, not stored: the row holds the kind and the facts, and the words
	// come from the copybook in the reader's language — which the fake spells out so this
	// asserts on the wiring rather than on the copy.
	if want := "title:order-delivered:vi-VN"; got.Title != want {
		t.Errorf("Title = %q, want %q", got.Title, want)
	}
	if got.Body != "body:ord_x" {
		t.Errorf("Body = %q, want the payload rendered", got.Body)
	}
	// The link comes from the kind's spec and the same payload, so a client never builds one.
	if want := "/account/orders/ord_x"; got.Href != want {
		t.Errorf("Href = %q, want %q", got.Href, want)
	}
	if got.ID == 0 {
		t.Error("the row has no id; without one a reader cannot mark just this one read")
	}
	if len(repo.notifs) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(repo.notifs))
	}
	if repo.notifs[0].AccountID != 42 {
		t.Errorf("AccountID = %d, want 42", repo.notifs[0].AccountID)
	}
	// Stored as the fact, so tomorrow's copy change reaches yesterday's rows.
	if repo.notifs[0].Kind != domain.KindOrderDelivered {
		t.Errorf("stored Kind = %q", repo.notifs[0].Kind)
	}
}

// The in-app channel is a preference like any other: turning it off means no row,
// not a hidden row, because the feed has no notion of invisible entries.
func TestCreateNotificationRespectsPreference(t *testing.T) {
	repo := newFakeRepo()
	repo.prefs[42] = []domain.Preference{{
		AccountID: 42,
		Category:  domain.CategoryOrder,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Kind:      string(domain.KindOrderDelivered),
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("wrote %d rows, want 0 — in-app is disabled", len(repo.notifs))
	}
}

// The category is not something a caller can get wrong any more: it follows from the kind. What
// a caller can still get wrong is the kind, and the domain is the only thing that knows the
// vocabulary — a tag on the request would be a second list to keep in step with the first.
func TestCreateNotificationRejectsUnknownKind(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Kind:      "gossip",
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
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
		Kind:      string(domain.KindOrderDelivered),
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
		Category:  domain.CategoryOrder,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}
	rec := &recorder{}
	svc := newTestServiceWithFanout(t, repo, rec)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Kind:      string(domain.KindOrderDelivered),
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(rec.sent) != 0 {
		t.Fatalf("pushed %d events, want 0", len(rec.sent))
	}
}

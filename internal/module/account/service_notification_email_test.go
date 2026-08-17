package account_test

import (
	"errors"
	"testing"

	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/id"
)

// The email channel of a notification. What is worth testing is the four things that have
// to hold before a letter goes out — the caller named a template, the account left the
// channel on, there is an address, and somebody proved it is theirs — plus the fact that
// none of them can fail the caller.

const mailAccountID = 42

// mailHarness is an account with a verified address and a service whose sends are visible.
func mailHarness(t *testing.T) (*account.Service, *fakeRepo, *fakeNotifier) {
	t.Helper()
	repo := newFakeRepo()
	repo.accounts[mailAccountID] = domain.Account{
		ID:            mailAccountID,
		Email:         new("buyer@example.com"),
		EmailVerified: true,
		Profile:       domain.Profile{Name: "Bùi", Country: "VN", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh"},
	}
	notes := &fakeNotifier{}
	return newTestServiceWithNotifier(t, repo, notes, noopFanout{}), repo, notes
}

func orderNotification() accountapi.CreateNotificationRequest {
	return accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](mailAccountID),
		Category:  string(domain.CategoryOrder),
		Title:     "Order placed",
		Payload:   map[string]any{"order_id": "ord_x", "total": int64(1250000), "currency": "VND"},
		MailKind:  string(notify.KindOrderPlaced),
	}
}

func TestCreateNotificationMailsTheAccount(t *testing.T) {
	svc, _, notes := mailHarness(t)

	if _, err := svc.CreateNotification(t.Context(), orderNotification()); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	sent := notes.sentOf(notify.KindOrderPlaced)
	if len(sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(sent))
	}
	m := sent[0]
	if m.Email != "buyer@example.com" {
		t.Errorf("Email = %q", m.Email)
	}
	// The locale decides which of the two copies goes out, so it has to come from the
	// recipient's profile rather than a default the caller happened to pass.
	if m.Locale != "vi-VN" {
		t.Errorf("Locale = %q, want the account's", m.Locale)
	}
	// The template renders the payload: sending anything else would let the mail and the
	// feed row disagree about the same sale.
	if m.Params["order_id"] != "ord_x" || m.Params["total"] != int64(1250000) {
		t.Errorf("Params = %v, want the notification's payload", m.Params)
	}
}

// A fact with no mail written for it is a feed row and nothing more.
func TestCreateNotificationWithoutAMailKindSendsNothing(t *testing.T) {
	svc, _, notes := mailHarness(t)

	req := orderNotification()
	req.MailKind = ""
	if _, err := svc.CreateNotification(t.Context(), req); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(notes.sent) != 0 {
		t.Fatalf("sent %d mails, want 0", len(notes.sent))
	}
}

// The preference matrix has had an email column since before anything read it. This is the
// test that it is now load-bearing.
func TestCreateNotificationRespectsTheEmailPreference(t *testing.T) {
	svc, repo, notes := mailHarness(t)
	repo.prefs[mailAccountID] = []domain.Preference{{
		AccountID: mailAccountID,
		Category:  domain.CategoryOrder,
		Channel:   domain.ChannelEmail,
		IsEnabled: false,
	}}

	if _, err := svc.CreateNotification(t.Context(), orderNotification()); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(notes.sent) != 0 {
		t.Fatalf("sent %d mails, want 0 — email is disabled", len(notes.sent))
	}
}

// The two channels are independent. Somebody who silenced the feed did not ask to stop
// being written to, and reading the in-app preference as a gate in front of the mail is the
// mistake this test exists to keep out.
func TestCreateNotificationMailsEvenWithTheFeedOff(t *testing.T) {
	svc, repo, notes := mailHarness(t)
	repo.prefs[mailAccountID] = []domain.Preference{{
		AccountID: mailAccountID,
		Category:  domain.CategoryOrder,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}

	if _, err := svc.CreateNotification(t.Context(), orderNotification()); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Errorf("wrote %d feed rows, want 0", len(repo.notifs))
	}
	if len(notes.sentOf(notify.KindOrderPlaced)) != 1 {
		t.Fatal("the mail did not go out")
	}
}

// An address nobody confirmed is somebody's typo or somebody else's inbox, and what goes
// out here is what they bought and what they paid.
func TestCreateNotificationSkipsAnUnverifiedAddress(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Account){
		"unverified": func(a *domain.Account) { a.EmailVerified = false },
		"no address": func(a *domain.Account) { a.Email, a.EmailVerified = nil, false },
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo, notes := mailHarness(t)
			row := repo.accounts[mailAccountID]
			mutate(&row)
			repo.accounts[mailAccountID] = row

			if _, err := svc.CreateNotification(t.Context(), orderNotification()); err != nil {
				t.Fatalf("CreateNotification: %v", err)
			}
			if len(notes.sent) != 0 {
				t.Fatalf("sent %d mails, want 0", len(notes.sent))
			}
			// The feed row is not conditional on an address — it is the channel that
			// needs nothing from the outside world.
			if len(repo.notifs) != 1 {
				t.Errorf("wrote %d feed rows, want 1", len(repo.notifs))
			}
		})
	}
}

// The caller is a bus subscriber: an error here is a redelivered order fact and a second
// feed row, paid to retry a mail nobody is waiting on.
func TestCreateNotificationSurvivesAFailedSend(t *testing.T) {
	svc, repo, notes := mailHarness(t)
	notes.err = errors.New("relay refused the connection")

	got, err := svc.CreateNotification(t.Context(), orderNotification())
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if got.Title != "Order placed" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(repo.notifs) != 1 {
		t.Errorf("wrote %d feed rows, want 1", len(repo.notifs))
	}
}

// The request's `oneof` list and the notify constants are the same set spelled twice, in two
// packages that cannot share the vocabulary — a struct tag takes no constant. This is what
// stops them drifting: a kind the subscriber emits that the request refuses would be a 400
// on a real order fact, found in production and nowhere else.
func TestCreateNotificationAcceptsEveryOrderMailKind(t *testing.T) {
	for _, kind := range []notify.Kind{
		notify.KindOrderPlaced,
		notify.KindOrderReceived,
		notify.KindOrderCompleted,
		notify.KindOrderCancelled,
		notify.KindRefundResolved,
		notify.KindOrderUnconfirmed,
	} {
		svc, _, notes := mailHarness(t)
		req := orderNotification()
		req.MailKind = string(kind)
		if _, err := svc.CreateNotification(t.Context(), req); err != nil {
			t.Errorf("CreateNotification(%s): %v", kind, err)
			continue
		}
		if len(notes.sentOf(kind)) != 1 {
			t.Errorf("kind %s did not reach the provider", kind)
		}
	}
}

// A template this deployment has no copy for must be refused at the request, not discovered
// by the provider when there is nothing left to do but log it.
func TestCreateNotificationRejectsAnUnknownMailKind(t *testing.T) {
	svc, _, notes := mailHarness(t)

	req := orderNotification()
	req.MailKind = "order-vanished"
	if _, err := svc.CreateNotification(t.Context(), req); status(t, err) != 400 {
		t.Fatalf("status = %d, want 400", status(t, err))
	}
	if len(notes.sent) != 0 {
		t.Errorf("sent %d mails, want 0", len(notes.sent))
	}
}

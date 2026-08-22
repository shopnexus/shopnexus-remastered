package account_test

import (
	"errors"
	"io/fs"
	"testing"

	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/lang"
	"shopnexus/templates"
)

// The email channel of a notification. What is worth testing is the four things that have
// to hold before a letter goes out — the kind has a template, the account left the channel on,
// there is an address, and somebody proved it is theirs — plus the fact that none of them can
// fail the caller.
//
// The template is no longer something a caller names: it follows from the kind, along with the
// category and the words. So the drift these tests used to guard — a title saying one thing and
// a mail template another — is now a shape that cannot be expressed.

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
		Kind:      string(domain.KindOrderPlaced),
		Payload:   map[string]any{"order_id": "ord_x", "total": int64(1250000), "currency": "VND"},
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

// A fact with no mail written for it is a feed row and nothing more. Which facts those are is
// the kind's own spec, so this is a property of the vocabulary rather than of a caller
// remembering to leave a field blank.
func TestCreateNotificationWithoutAMailTemplateSendsNothing(t *testing.T) {
	svc, _, notes := mailHarness(t)

	req := orderNotification()
	req.Kind = string(domain.KindSaleHandedOver)
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
	if got.Title == "" {
		t.Error("the returned row has no title; a failed send must not cost the words too")
	}
	if len(repo.notifs) != 1 {
		t.Errorf("wrote %d feed rows, want 1", len(repo.notifs))
	}
}

// Every kind that claims a mail template actually reaches the provider under that template.
// Walked from the vocabulary rather than a hand-kept list: a kind added with a `Mail` nobody
// wired would otherwise be a letter that silently never goes out.
func TestCreateNotificationMailsEveryKindThatClaimsATemplate(t *testing.T) {
	for _, kind := range domain.Kinds {
		spec, _ := domain.SpecOf(kind)
		if spec.Mail == "" {
			continue
		}
		svc, _, notes := mailHarness(t)
		req := orderNotification()
		req.Kind = string(kind)
		if _, err := svc.CreateNotification(t.Context(), req); err != nil {
			t.Errorf("CreateNotification(%s): %v", kind, err)
			continue
		}
		if len(notes.sentOf(notify.Kind(spec.Mail))) != 1 {
			t.Errorf("kind %s did not reach the provider as template %q", kind, spec.Mail)
		}
	}
}

// Every template a kind names has copy on disk, in every language. The two lists live in
// different packages — a domain may not import a provider — so this is what keeps them in step:
// a typo here used to be a mail that failed at 3am on the one night it mattered.
func TestKindSpecs_MailTemplatesExist(t *testing.T) {
	mails := templates.Mail()
	for _, kind := range domain.Kinds {
		spec, _ := domain.SpecOf(kind)
		if spec.Mail == "" {
			continue
		}
		for _, l := range lang.All {
			file := spec.Mail + "." + l + ".html"
			if _, err := fs.Stat(mails, file); err != nil {
				t.Errorf("kind %s names mail template %q, which has no %s copy", kind, spec.Mail, l)
			}
		}
	}
}

// A kind nobody has copy for must be refused where it arrives, not discovered by a reader
// staring at a blank row.
func TestCreateNotificationRejectsAnUnknownKind(t *testing.T) {
	svc, repo, notes := mailHarness(t)

	req := orderNotification()
	req.Kind = "order-vanished"
	if _, err := svc.CreateNotification(t.Context(), req); status(t, err) != 400 {
		t.Fatalf("status = %d, want 400", status(t, err))
	}
	if len(repo.notifs) != 0 || len(notes.sent) != 0 {
		t.Errorf("wrote %d rows and sent %d mails, want none of either", len(repo.notifs), len(notes.sent))
	}
}

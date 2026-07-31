package domain_test

import (
	"errors"
	"testing"

	"shopnexus/internal/module/account/domain"
)

// linked reconstitutes a root with its links attached, the way the adapter does — a
// provider-only account is only legal once one is there, so it is not built through the
// constructor.
func linked(t *testing.T, password string, providers ...string) *domain.Account {
	t.Helper()
	email := "a@b.com"
	acc := &domain.Account{
		ID: 1, Version: 1,
		Status:  domain.StatusActive,
		Role:    domain.RoleUser,
		Email:   &email,
		Profile: testProfile(t),
	}
	if password != "" {
		acc.PasswordHash = &password
	}
	acc.Profile.ID = 1
	for i, p := range providers {
		acc.Identities = append(acc.Identities, &domain.OAuthIdentity{
			ID: int64(i + 1), AccountID: 1, Provider: p, ProviderUID: p + "-sub",
		})
	}
	return acc
}

// The rule the root exists for. Unlinking is allowed only while another way in survives,
// and the root is the only place that can see both halves — the password and the links.
func TestUnlink_KeepsTheLastSignInMethod(t *testing.T) {
	for _, tc := range []struct {
		name      string
		password  string
		providers []string
		unlink    string
		wantErr   error
	}{
		{name: "last provider, no password", providers: []string{"google"}, unlink: "google", wantErr: domain.ErrLastSignInMethod},
		{name: "one of two providers", providers: []string{"google", "apple"}, unlink: "google"},
		{name: "last provider but a password remains", password: "hash", providers: []string{"google"}, unlink: "google"},
		{name: "provider not linked", password: "hash", providers: []string{"google"}, unlink: "apple", wantErr: domain.ErrOAuthIdentityNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := linked(t, tc.password, tc.providers...)
			err := acc.Unlink(tc.unlink)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Unlink = %v, want %v", err, tc.wantErr)
			}
			want := len(tc.providers)
			if tc.wantErr == nil {
				want--
			}
			if got := len(acc.Identities); got != want {
				t.Errorf("identities = %d, want %d", got, want)
			}
		})
	}
}

// Unlinking down to the last one, then trying again. The second attempt is refused because
// the root decides against the set it holds rather than against a count read separately.
func TestUnlink_SecondOneIsRefused(t *testing.T) {
	acc := linked(t, "", "google", "apple")
	if err := acc.Unlink("google"); err != nil {
		t.Fatalf("first Unlink: %v", err)
	}
	if err := acc.Unlink("apple"); !errors.Is(err, domain.ErrLastSignInMethod) {
		t.Fatalf("second Unlink = %v, want ErrLastSignInMethod", err)
	}
}

// The backstop behind the exported slice: an account emptied by hand is refused at the
// write, so nothing has to trust the caller.
func TestValidate_RefusesAnAccountWithNoWayIn(t *testing.T) {
	acc := linked(t, "", "google")
	acc.Identities = nil
	if got := status(t, acc.Validate()); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

func TestLink_OneIdentityPerProvider(t *testing.T) {
	acc := linked(t, "hash", "google")
	if _, err := acc.Link("google", "another-sub"); !errors.Is(err, domain.ErrIdentifierTaken) {
		t.Fatalf("Link = %v, want ErrIdentifierTaken", err)
	}
	added, err := acc.Link("apple", "sub-2")
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if added.AccountID != acc.ID {
		t.Errorf("added.AccountID = %d, want %d", added.AccountID, acc.ID)
	}
	if got := len(acc.Identities); got != 2 {
		t.Errorf("identities = %d, want 2", got)
	}
	// The caller keeps the pointer the aggregate holds, so an id filled in at Save is
	// visible without a second read.
	if acc.Identities[1] != added {
		t.Error("Link handed back a copy rather than the row it appended")
	}
}

// Two links of the same provider cannot be smuggled past Link by appending directly.
func TestValidate_RefusesDuplicateProviders(t *testing.T) {
	acc := linked(t, "hash", "google")
	acc.Identities = append(acc.Identities, &domain.OAuthIdentity{AccountID: 1, Provider: "google", ProviderUID: "other"})
	if !errors.Is(acc.Validate(), domain.ErrIdentifierTaken) {
		t.Fatalf("Validate = %v, want ErrIdentifierTaken", acc.Validate())
	}
}

// Events are facts, not instructions: they name what happened and carry the diff the audit
// row keeps.
func TestEvents_RecordWhatHappened(t *testing.T) {
	acc := linked(t, "hash")
	acc.SetRole(domain.RoleModerator)
	acc.SetPassword("new-hash")

	events := acc.Events()
	if len(events) != 2 {
		t.Fatalf("events = %v, want two", events)
	}
	// The payload comes back at the type the event type declares, and only for that fact.
	change, ok := domain.PayloadOf(events[0], domain.RoleGranted)
	if !ok || change.Role != domain.RoleModerator {
		t.Errorf("first event = %+v, payload = %+v ok = %v", events[0], change, ok)
	}
	if _, ok := domain.PayloadOf(events[0], domain.EmailChanged); ok {
		t.Error("PayloadOf read a payload out of a different fact")
	}
	if !acc.Happened(domain.PasswordChanged.Code) {
		t.Error("the password change was not recorded")
	}
	acc.ClearEvents()
	if len(acc.Events()) != 0 {
		t.Error("ClearEvents left something behind")
	}
}

// Setting the same role is not a change, so it records nothing.
func TestSetRole_NoOpRecordsNothing(t *testing.T) {
	acc := linked(t, "hash")
	acc.SetRole(domain.RoleUser)
	if len(acc.Events()) != 0 {
		t.Fatalf("events = %v, want none", acc.Events())
	}
}

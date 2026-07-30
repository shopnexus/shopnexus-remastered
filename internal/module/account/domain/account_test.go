package domain_test

import (
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/errx"
)

func ptr[T any](v T) *T { return &v }

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

// An account is addressable by any one of the three identifiers, and the values are
// normalized on the way in so the UNIQUE columns and the sign-in lookup agree.
func TestNewAccount_AnyOneIdentifierIsEnough(t *testing.T) {
	for _, tc := range []struct{ name, email, phone, username string }{
		{name: "email", email: "A@B.com"},
		{name: "phone", phone: "+84901234567"},
		{name: "username", username: "Alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := domain.NewAccount(domain.RoleUser, tc.email, tc.phone, tc.username, "hash")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !a.HasIdentifier() {
				t.Fatal("HasIdentifier() = false")
			}
			if tc.email != "" && a.Email != "a@b.com" {
				t.Errorf("email = %q, want lowercase", a.Email)
			}
			if tc.username != "" && a.Username != "alice" {
				t.Errorf("username = %q, want lowercase", a.Username)
			}
			if a.Status != domain.StatusActive || a.Role != domain.RoleUser {
				t.Errorf("account = %+v, want an active user", a)
			}
		})
	}
}

// An account nobody can be addressed by cannot sign in, so it is not a state that exists.
func TestNewAccount_NoIdentifierRejected(t *testing.T) {
	_, err := domain.NewAccount(domain.RoleUser, "", "", "", "hash")
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A provider-only account has no password, which is what HasPassword reports and what
// makes unlinking the last provider refusable.
func TestNewAccount_PasswordIsOptional(t *testing.T) {
	a, err := domain.NewAccount(domain.RoleUser, "a@b.com", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.HasPassword() {
		t.Fatal("HasPassword() = true for a provider-only account")
	}
}

func TestNewAccount_MalformedIdentifiersRejected(t *testing.T) {
	for _, tc := range []struct{ name, email, phone, username string }{
		{name: "email", email: "not-an-email"},
		{name: "phone", phone: "0901234567"}, // not E.164
		{name: "username too short", username: "ab"},
		{name: "username charset", username: "Ali ce!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewAccount(domain.RoleUser, tc.email, tc.phone, tc.username, "hash"); status(t, err) != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
		})
	}
}

// Removing the last identifier is refused, and the entity is left exactly as it was so the
// caller can report the error and carry on.
func TestSetEmail_LastIdentifierRefusedAndRolledBack(t *testing.T) {
	a, err := domain.NewAccount(domain.RoleUser, "a@b.com", "", "", "hash")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	if got := status(t, a.SetEmail(nil)); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
	if a.Email != "a@b.com" {
		t.Fatalf("email = %q, want the change rolled back", a.Email)
	}
}

// A new address is unverified by definition: keeping the flag would let anyone claim a
// verified address by editing it.
func TestSetEmail_ClearsVerified(t *testing.T) {
	a, _ := domain.NewAccount(domain.RoleUser, "a@b.com", "+84901234567", "", "hash")
	a.EmailVerified = true

	if err := a.SetEmail(ptr("c@d.com")); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}
	if a.EmailVerified {
		t.Fatal("email_verified survived an address change")
	}
	if a.Email != "c@d.com" {
		t.Fatalf("email = %q", a.Email)
	}
}

// Re-sending the same address is not a change, so the verified flag stays.
func TestSetEmail_SameAddressKeepsVerified(t *testing.T) {
	a, _ := domain.NewAccount(domain.RoleUser, "a@b.com", "", "", "hash")
	a.EmailVerified = true

	if err := a.SetEmail(ptr("A@B.com")); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}
	if !a.EmailVerified {
		t.Fatal("email_verified was cleared by a no-op change")
	}
}

// Removing one identifier while another remains is fine — that is the point of having three.
func TestSetPhone_RemovableWhileAnotherIdentifierRemains(t *testing.T) {
	a, _ := domain.NewAccount(domain.RoleUser, "a@b.com", "+84901234567", "", "hash")
	if err := a.SetPhone(nil); err != nil {
		t.Fatalf("SetPhone: %v", err)
	}
	if a.Phone != "" {
		t.Fatalf("phone = %q, want cleared", a.Phone)
	}
}

func TestIsSuspended(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	for _, tc := range []struct {
		name  string
		build func() domain.Account
		want  bool
	}{
		{name: "active", build: func() domain.Account { return domain.Account{Status: domain.StatusActive} }, want: false},
		{
			name:  "permanent",
			build: func() domain.Account { a := domain.Account{}; a.Suspend("scam", nil); return a },
			want:  true,
		},
		{
			name:  "still running",
			build: func() domain.Account { a := domain.Account{}; a.Suspend("scam", &future); return a },
			want:  true,
		},
		{
			// Nothing rewrites the row when the clock runs out, so the deadline has to be read.
			name:  "deadline passed",
			build: func() domain.Account { a := domain.Account{}; a.Suspend("scam", &past); return a },
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.build().IsSuspended(now); got != tc.want {
				t.Fatalf("IsSuspended() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Reinstating clears the details with the status: the row keeps only the suspension in
// force, and past ones live in the audit log.
func TestReinstate_ClearsTheDetails(t *testing.T) {
	a := domain.Account{}
	until := time.Now().Add(time.Hour)
	a.Suspend("scam", &until)
	a.Reinstate()

	if a.Status != domain.StatusActive || a.SuspensionReason != "" || a.SuspendedUntil != nil {
		t.Fatalf("account = %+v, want a clean active row", a)
	}
}

func TestGenerateUsername_ValidAndUnique(t *testing.T) {
	first, err := domain.GenerateUsername()
	if err != nil {
		t.Fatalf("GenerateUsername: %v", err)
	}
	// It has to satisfy the same rules as one a user picks, or the insert fails.
	if _, err := domain.NewAccount(domain.RoleUser, "", "", first, ""); err != nil {
		t.Fatalf("generated username %q is not valid: %v", first, err)
	}
	second, _ := domain.GenerateUsername()
	if first == second {
		t.Fatal("two generated usernames collided")
	}
}

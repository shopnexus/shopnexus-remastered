package domain_test

import (
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/errx"
)

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

// testProfile is the display half of the aggregate, valid and uninteresting.
func testProfile(t *testing.T) domain.Profile {
	t.Helper()
	p, err := domain.NewProfile("Alice", "VN", "vi-VN", "Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	return p
}

func newAccount(t *testing.T, email, phone, username, hash string) *domain.Account {
	t.Helper()
	a, err := domain.NewAccount(domain.RoleUser, email, phone, username, hash, testProfile(t))
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return a
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
			a := newAccount(t, tc.email, tc.phone, tc.username, "hash")
			if !a.HasIdentifier() {
				t.Fatal("HasIdentifier() = false")
			}
			if tc.email != "" {
				if a.Email == nil {
					t.Error("email was not stored")
				} else if *a.Email != "a@b.com" {
					t.Errorf("email = %q, want it lowercased", *a.Email)
				}
			}
			if tc.username != "" {
				if a.Username == nil {
					t.Error("username was not stored")
				} else if *a.Username != "alice" {
					t.Errorf("username = %q, want it lowercased", *a.Username)
				}
			}
			if a.Status != domain.StatusActive || a.Role != domain.RoleUser {
				t.Errorf("account = %+v, want an active user", a)
			}
			if a.Version != 1 {
				t.Errorf("version = %d, want 1 — Save has a value to check against", a.Version)
			}
		})
	}
}

// An account nobody can be addressed by cannot sign in, so it is not a state that exists.
func TestNewAccount_NoIdentifierRejected(t *testing.T) {
	_, err := domain.NewAccount(domain.RoleUser, "", "", "", "hash", testProfile(t))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A provider-only account has no password, which is what HasPassword reports and what
// makes unlinking the last provider refusable. It still needs a way in, so the constructor
// counts the password *and* the links.
func TestNewAccount_PasswordIsOptionalButNotFreeOfCharge(t *testing.T) {
	_, err := domain.NewAccount(domain.RoleUser, "a@b.com", "", "", "", testProfile(t))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422 for an account with no password and no link", got)
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
			_, err := domain.NewAccount(domain.RoleUser, tc.email, tc.phone, tc.username, "hash", testProfile(t))
			if status(t, err) != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
		})
	}
}

// An invalid profile is an invalid account: the aggregate validates as one thing, so a
// caller cannot store a display name the profile's own rules refuse.
func TestValidate_CoversTheProfile(t *testing.T) {
	a := newAccount(t, "a@b.com", "", "", "hash")
	a.Profile.Name = ""
	if got := status(t, a.Validate()); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// Clearing the last identifier is refused at Validate — which is where Save asks, so the
// change never reaches the database. The entity is left holding the illegal value on
// purpose: the command is abandoned, not repaired.
func TestClearEmail_LastIdentifierIsRefusedAtValidate(t *testing.T) {
	a := newAccount(t, "a@b.com", "", "", "hash")
	a.ClearEmail()
	if got := status(t, a.Validate()); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A new address is unverified by definition: keeping the flag would let anyone claim a
// verified address by editing it.
func TestSetEmail_ClearsVerifiedAndRecordsIt(t *testing.T) {
	a := newAccount(t, "a@b.com", "+84901234567", "", "hash")
	a.EmailVerified = true

	a.SetEmail("c@d.com")
	if a.EmailVerified {
		t.Fatal("email_verified survived an address change")
	}
	if a.Email == nil || *a.Email != "c@d.com" {
		t.Fatalf("email = %v, want c@d.com", a.Email)
	}
	if !a.Happened(domain.EmailChanged.Code) {
		t.Fatal("the change was not recorded")
	}
}

// Re-sending the same address is not a change, so the verified flag stays and nothing is
// recorded — which is what lets a caller ask "did this command change the email".
func TestSetEmail_SameAddressIsNotAChange(t *testing.T) {
	a := newAccount(t, "a@b.com", "", "", "hash")
	a.EmailVerified = true

	a.SetEmail("A@B.com")
	if !a.EmailVerified {
		t.Fatal("email_verified was cleared by a no-op change")
	}
	if a.Happened(domain.EmailChanged.Code) {
		t.Fatal("a no-op was recorded as a change")
	}
}

// Removing one identifier while another remains is fine — that is the point of having three.
func TestClearPhone_AllowedWhileAnotherIdentifierRemains(t *testing.T) {
	a := newAccount(t, "a@b.com", "+84901234567", "", "hash")
	a.ClearPhone()
	if a.Phone != nil {
		t.Fatalf("phone = %q, want cleared", *a.Phone)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestIsSuspended(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	for _, tc := range []struct {
		name  string
		build func() *domain.Account
		want  bool
	}{
		{name: "active", build: func() *domain.Account { return &domain.Account{Status: domain.StatusActive} }, want: false},
		{
			name:  "permanent",
			build: func() *domain.Account { a := &domain.Account{}; a.Suspend("scam", nil); return a },
			want:  true,
		},
		{
			name:  "still running",
			build: func() *domain.Account { a := &domain.Account{}; a.Suspend("scam", &future); return a },
			want:  true,
		},
		{
			// Nothing rewrites the row when the clock runs out, so the deadline has to be read.
			name:  "deadline passed",
			build: func() *domain.Account { a := &domain.Account{}; a.Suspend("scam", &past); return a },
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
	a := &domain.Account{}
	until := time.Now().Add(time.Hour)
	a.Suspend("scam", &until)
	a.Reinstate()

	if a.Status != domain.StatusActive || a.SuspensionReason != nil || a.SuspendedUntil != nil {
		t.Fatalf("account = %+v, want a clean active row", a)
	}
	if !a.Happened(domain.Suspended.Code) || !a.Happened(domain.Reinstated.Code) {
		t.Fatalf("events = %v, want both recorded", a.Events())
	}
}

func TestGenerateUsername_ValidAndUnique(t *testing.T) {
	first, err := domain.GenerateUsername()
	if err != nil {
		t.Fatalf("GenerateUsername: %v", err)
	}
	// It has to satisfy the same rules as one a user picks, or the insert fails.
	if _, err := domain.NewAccount(domain.RoleUser, "", "", first, "hash", testProfile(t)); err != nil {
		t.Fatalf("generated username %q is not valid: %v", first, err)
	}
	second, _ := domain.GenerateUsername()
	if first == second {
		t.Fatal("two generated usernames collided")
	}
}

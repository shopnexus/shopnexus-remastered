package account_test

import (
	"context"
	"slices"
	"testing"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
)

// promote grants a role directly, which is how an admin exists at all: the role is
// configured, not claimed.
func (h *harness) promote(t *testing.T, accountID id.ID[id.Account], role domain.Role) {
	t.Helper()
	ctx := context.Background()
	acc, err := h.repo.Get(ctx, accountID.Int64())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	acc.SetRole(role)
	if err := h.repo.Save(ctx, acc, acc.ID); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// staff registers an account and gives it a role, returning its sign-in.
func (h *harness) staff(t *testing.T, email string, role domain.Role) accountapi.AuthResult {
	t.Helper()
	req := registerRequest()
	req.Email = email
	// A distinct display name, so a test searching for a user by name fragment does not also
	// match the moderator that ran the search.
	req.Name = "Staff"
	res := h.register(t, req)
	h.promote(t, res.Account.ID, role)
	return res
}

// A plain user cannot read the moderator console, and the check is the service's because
// the role is a row in this module's table.
func TestAdmin_PlainUserRefused(t *testing.T) {
	h := newHarness()
	user := h.register(t, registerRequest())
	ctx := context.Background()

	_, err := h.svc.AdminListAccounts(ctx, accountapi.AdminListAccountsRequest{ActorID: user.Account.ID, Page: 1, Limit: 20})
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// An admin outranks a moderator, so it passes every moderator check too.
func TestAdmin_AdminPassesModeratorChecks(t *testing.T) {
	h := newHarness()
	admin := h.staff(t, "admin@example.com", domain.RoleAdmin)

	if _, err := h.svc.AdminListAccounts(context.Background(), accountapi.AdminListAccountsRequest{
		ActorID: admin.Account.ID, Page: 1, Limit: 20,
	}); err != nil {
		t.Fatalf("AdminListAccounts: %v", err)
	}
}

// Provisioning a moderator is admin-only: a moderator who could appoint moderators is an
// escalation path.
func TestAdminCreateModerator_ModeratorRefused(t *testing.T) {
	h := newHarness()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)

	_, err := h.svc.AdminCreateModerator(context.Background(), accountapi.CreateModeratorRequest{
		ActorID: moderator.Account.ID, Email: "mod2@example.com", Password: "password1",
		Name: "Mod Two", Country: "VN", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh",
	})
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestAdminCreateModerator_ByAdmin(t *testing.T) {
	h := newHarness()
	admin := h.staff(t, "admin@example.com", domain.RoleAdmin)

	created, err := h.svc.AdminCreateModerator(context.Background(), accountapi.CreateModeratorRequest{
		ActorID: admin.Account.ID, Email: "mod@example.com", Password: "password1",
		Name: "Mod", Country: "VN", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh",
	})
	if err != nil {
		t.Fatalf("AdminCreateModerator: %v", err)
	}
	if created.Role != string(domain.RoleModerator) || created.Name != "Mod" {
		t.Fatalf("account = %+v", created)
	}
	if !slices.Contains(h.repo.codes(), string(domain.RoleGranted.Code)) {
		t.Errorf("audit codes = %v, want the grant recorded", h.repo.codes())
	}
}

// Suspending has to drop the account's sessions: a suspended row does not stop an access
// token already in circulation, so on its own it suspends the account on paper only.
func TestAdminSuspendAccount_DropsSessionsAndAudits(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	target := h.register(t, registerRequest())
	targetSession := h.sessionID(t, target.AccessToken)

	until := time.Now().Add(48 * time.Hour)
	got, err := h.svc.AdminSuspendAccount(ctx, accountapi.SuspendAccountRequest{
		ActorID: moderator.Account.ID, AccountID: target.Account.ID, Reason: "scam", Until: &until,
	})
	if err != nil {
		t.Fatalf("AdminSuspendAccount: %v", err)
	}
	if got.Status != string(domain.StatusSuspended) || got.SuspensionReason == nil || *got.SuspensionReason != "scam" {
		t.Fatalf("account = %+v", got)
	}
	if _, err := h.sessions.Lookup(ctx, targetSession); err == nil {
		t.Error("the suspended account still has a live session")
	}
	// The trail carries the decision at the type the event declares, so an assertion reads
	// a field rather than guessing a map key.
	diff, ok := auditedDiff(h.repo, domain.Suspended)
	if !ok {
		t.Fatalf("audit codes = %v, want the suspension recorded", h.repo.codes())
	}
	if diff.Reason != "scam" || diff.Until == nil || !diff.Until.Equal(until) {
		t.Errorf("recorded suspension = %+v, want the reason and the deadline", diff)
	}
}

// Lifting clears the reason and the deadline with the status: the row carries only the
// suspension in force.
func TestAdminLiftSuspension_ClearsTheDetails(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	target := h.register(t, registerRequest())

	if _, err := h.svc.AdminSuspendAccount(ctx, accountapi.SuspendAccountRequest{
		ActorID: moderator.Account.ID, AccountID: target.Account.ID, Reason: "scam",
	}); err != nil {
		t.Fatalf("AdminSuspendAccount: %v", err)
	}
	got, err := h.svc.AdminLiftSuspension(ctx, accountapi.LiftSuspensionRequest{
		ActorID: moderator.Account.ID, AccountID: target.Account.ID,
	})
	if err != nil {
		t.Fatalf("AdminLiftSuspension: %v", err)
	}
	if got.Status != string(domain.StatusActive) || got.SuspensionReason != nil || got.SuspendedUntil != nil {
		t.Fatalf("account = %+v, want a clean active row", got)
	}
	// The lifted suspension stays in the audit log rather than on the row.
	if !slices.Contains(h.repo.codes(), string(domain.Reinstated.Code)) {
		t.Errorf("audit codes = %v", h.repo.codes())
	}
}

// Revoking demotes and drops the sessions, and the account survives because it may have
// traded.
func TestAdminRevokeModerator_DemotesAndDropsSessions(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	admin := h.staff(t, "admin@example.com", domain.RoleAdmin)
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	modSession := h.sessionID(t, moderator.AccessToken)

	if err := h.svc.AdminRevokeModerator(ctx, accountapi.RevokeModeratorRequest{
		ActorID: admin.Account.ID, AccountID: moderator.Account.ID,
	}); err != nil {
		t.Fatalf("AdminRevokeModerator: %v", err)
	}
	acc, err := h.repo.Get(ctx, moderator.Account.ID.Int64())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acc.Role != domain.RoleUser {
		t.Errorf("role = %q, want user", acc.Role)
	}
	if _, err := h.sessions.Lookup(ctx, modSession); err == nil {
		t.Error("the demoted moderator kept a live session")
	}
}

// The resource being addressed is the moderator, so an account that is not one is simply
// not there.
func TestAdminRevokeModerator_PlainUserIsNotFound(t *testing.T) {
	h := newHarness()
	admin := h.staff(t, "admin@example.com", domain.RoleAdmin)
	user := h.register(t, registerRequest())

	err := h.svc.AdminRevokeModerator(context.Background(), accountapi.RevokeModeratorRequest{
		ActorID: admin.Account.ID, AccountID: user.Account.ID,
	})
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// The search takes an exact identifier or a fragment of a display name, and it is one query
// either way — the caller does not choose.
func TestAdminListAccounts_MatchesIdentifierOrName(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	target := h.register(t, registerRequest())

	for _, q := range []string{"alice@example.com", "Ali"} {
		page, err := h.svc.AdminListAccounts(ctx, accountapi.AdminListAccountsRequest{
			ActorID: moderator.Account.ID, Query: q, Page: 1, Limit: 20,
		})
		if err != nil {
			t.Fatalf("AdminListAccounts(%q): %v", q, err)
		}
		if len(page.Data) != 1 || page.Data[0].ID != target.Account.ID {
			t.Fatalf("query %q returned %+v", q, page.Data)
		}
		if page.Meta.TotalCount == nil || *page.Meta.TotalCount != 1 {
			t.Fatalf("total = %v, want 1", page.Meta.TotalCount)
		}
	}
}

// --- payout identity verification ---

// The queue entry carries the subject: a moderator cannot decide a case about an account
// they cannot see.
func TestIdentityVerdict_VerifiesThenBlocksASecondLiveDocument(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	user := h.register(t, registerRequest())

	ticket, err := h.svc.StartIdentityVerification(ctx, scanRequest(user.Account.ID, domain.DocTypeNationalID))
	if err != nil {
		t.Fatalf("StartIdentityVerification: %v", err)
	}
	if ticket.Document.Status != string(domain.IdentityPending) || ticket.VendorSessionURL == nil {
		t.Fatalf("ticket = %+v, want a pending case with a vendor session", ticket)
	}

	queue, err := h.svc.AdminListIdentityDocuments(ctx, accountapi.AdminListIdentityDocumentsRequest{
		ActorID: moderator.Account.ID, Status: string(domain.IdentityPending), Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("AdminListIdentityDocuments: %v", err)
	}
	if len(queue.Data) != 1 || queue.Data[0].Account.ID != user.Account.ID {
		t.Fatalf("queue = %+v, want the case with its subject", queue.Data)
	}

	verdict, err := h.svc.AdminRecordIdentityVerdict(ctx, accountapi.IdentityVerdictRequest{
		ActorID: moderator.Account.ID, DocumentID: ticket.Document.ID, Status: string(domain.IdentityVerified),
	})
	if err != nil {
		t.Fatalf("AdminRecordIdentityVerdict: %v", err)
	}
	if verdict.Status != string(domain.IdentityVerified) || verdict.VerifiedAt == nil {
		t.Fatalf("document = %+v", verdict)
	}
	// The payout gate reads this, so it has to flip with the verdict.
	me, err := h.svc.GetMe(ctx, accountapi.GetMeRequest{ActorID: user.Account.ID})
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if !me.IdentityVerified {
		t.Error("identity_verified = false after a verified document")
	}
	// A second case is refused while a live one exists.
	if _, err := h.svc.StartIdentityVerification(ctx, scanRequest(user.Account.ID, domain.DocTypePassport)); status(t, err) != 409 {
		t.Errorf("status = %v, want 409 for a second live document", err)
	}
}

// A passport expires, so approving one without an expiry would leave the gate reading a
// status that stays true forever.
func TestIdentityVerdict_PassportNeedsAnExpiry(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	user := h.register(t, registerRequest())

	ticket, err := h.svc.StartIdentityVerification(ctx, scanRequest(user.Account.ID, domain.DocTypePassport))
	if err != nil {
		t.Fatalf("StartIdentityVerification: %v", err)
	}
	_, err = h.svc.AdminRecordIdentityVerdict(ctx, accountapi.IdentityVerdictRequest{
		ActorID: moderator.Account.ID, DocumentID: ticket.Document.ID, Status: string(domain.IdentityVerified),
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}

	expires := time.Now().AddDate(5, 0, 0)
	if _, err := h.svc.AdminRecordIdentityVerdict(ctx, accountapi.IdentityVerdictRequest{
		ActorID: moderator.Account.ID, DocumentID: ticket.Document.ID,
		Status: string(domain.IdentityVerified), ExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("AdminRecordIdentityVerdict with an expiry: %v", err)
	}
}

func TestIdentityVerdict_RejectionNeedsAReason(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	moderator := h.staff(t, "mod@example.com", domain.RoleModerator)
	user := h.register(t, registerRequest())
	ticket, err := h.svc.StartIdentityVerification(ctx, scanRequest(user.Account.ID, domain.DocTypeNationalID))
	if err != nil {
		t.Fatalf("StartIdentityVerification: %v", err)
	}

	_, err = h.svc.AdminRecordIdentityVerdict(ctx, accountapi.IdentityVerdictRequest{
		ActorID: moderator.Account.ID, DocumentID: ticket.Document.ID, Status: string(domain.IdentityRejected),
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}

	doc, err := h.svc.AdminRecordIdentityVerdict(ctx, accountapi.IdentityVerdictRequest{
		ActorID: moderator.Account.ID, DocumentID: ticket.Document.ID,
		Status: string(domain.IdentityRejected), RejectionReason: "blurry scan",
	})
	if err != nil {
		t.Fatalf("AdminRecordIdentityVerdict: %v", err)
	}
	if doc.RejectionReason == nil || *doc.RejectionReason != "blurry scan" {
		t.Fatalf("document = %+v", doc)
	}
	// A rejected case is still in the account's own history: a rejection followed by a fresh
	// attempt is the normal path.
	history, err := h.svc.ListIdentityDocuments(ctx, accountapi.ListIdentityDocumentsRequest{ActorID: user.Account.ID})
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %+v, err = %v", history, err)
	}
}

// scanRequest names the three uploads a real submission carries. The ids are arbitrary:
// what matters is that the service resolves each one to a fetch URL before calling the
// vendor.
func scanRequest(actorID id.ID[id.Account], docType domain.DocType) accountapi.StartIdentityVerificationRequest {
	return accountapi.StartIdentityVerificationRequest{
		ActorID:          actorID,
		DocType:          string(docType),
		FrontResourceID:  id.Of[id.Resource](11),
		BackResourceID:   id.Of[id.Resource](12),
		SelfieResourceID: id.Of[id.Resource](13),
	}
}

// A scan whose upload was never confirmed has no fetch URL, and the vendor must not be
// called with a blank one: the account is told to re-upload instead.
func TestStartIdentityVerification_UnavailableScanRefused(t *testing.T) {
	h := newHarness()
	user := h.register(t, registerRequest())
	h.repo.missingResources[13] = true // the selfie

	_, err := h.svc.StartIdentityVerification(context.Background(), scanRequest(user.Account.ID, domain.DocTypeNationalID))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// The dev vendor leaves the case pending, which is what puts it in the moderator queue —
// the same place a real vendor's "I could not decide" lands.
func TestStartIdentityVerification_MockVendorLeavesItPending(t *testing.T) {
	h := newHarness()
	user := h.register(t, registerRequest())

	ticket, err := h.svc.StartIdentityVerification(context.Background(), scanRequest(user.Account.ID, domain.DocTypeNationalID))
	if err != nil {
		t.Fatalf("StartIdentityVerification: %v", err)
	}
	if ticket.Document.Status != string(domain.IdentityPending) {
		t.Fatalf("status = %q, want pending", ticket.Document.Status)
	}
	if ticket.Document.Provider != "mock" || ticket.Document.ID == 0 {
		t.Fatalf("document = %+v, want the vendor named and a stored row", ticket.Document)
	}
}

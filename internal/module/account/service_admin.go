package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// AdminListAccounts is the moderator's search. The rows come back as accounts, and the
// display names, the identity flags and nothing else are resolved in one extra call
// each — three queries per page rather than a join the repository would have to keep in
// step with two other read shapes.
func (s *Service) AdminListAccounts(ctx context.Context, req accountapi.AdminListAccountsRequest) (accountapi.Page[accountapi.AdminAccount], error) {
	if _, err := s.requireModerator(ctx, req.ActorID); err != nil {
		return accountapi.Page[accountapi.AdminAccount]{}, err
	}
	rows, total, err := s.repo.SearchAccounts(ctx, port.AccountFilter{
		Query:  domain.NormalizeEmail(req.Query),
		Status: domain.Status(req.Status),
		Role:   domain.Role(req.Role),
		Offset: offsetOf(req.Page, req.Limit),
		Limit:  req.Limit,
	})
	if err != nil {
		return accountapi.Page[accountapi.AdminAccount]{}, fmt.Errorf("search accounts: %w", err)
	}
	out, err := s.toAdminAccounts(ctx, rows)
	if err != nil {
		return accountapi.Page[accountapi.AdminAccount]{}, err
	}
	return accountapi.Page[accountapi.AdminAccount]{
		Data: out,
		Meta: accountapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// AdminSuspendAccount is the outcome of an upheld report. The row *and* the sessions go
// together: a suspended row does not stop an access token that is already in
// circulation, so leaving the sessions alive would suspend the account on paper only.
func (s *Service) AdminSuspendAccount(ctx context.Context, req accountapi.SuspendAccountRequest) (accountapi.AdminAccount, error) {
	moderator, err := s.requireModerator(ctx, req.ActorID)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	target, err := s.repo.FindAccountByID(ctx, req.AccountID.Int64())
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("find account by id: %w", err)
	}
	target.Suspend(req.Reason, req.Until)
	if err := s.repo.UpdateAccountStatus(ctx, target); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("update account status: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, target.ID, ""); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("revoke sessions: %w", err)
	}
	s.audit(ctx, port.AuditEntry{
		Table:      "account",
		RecordID:   target.ID,
		ChangeType: "update",
		Code:       "account.suspend",
		ChangedBy:  moderator.ID,
		Diff:       map[string]any{"status": string(target.Status), "suspension_reason": req.Reason, "suspended_until": req.Until},
		Snapshot:   accountSnapshot(target),
	})
	return s.adminAccount(ctx, target)
}

// AdminLiftSuspension clears the reason and the deadline with the status: the row keeps
// only the suspension in force, and the one just lifted is in the audit log.
func (s *Service) AdminLiftSuspension(ctx context.Context, req accountapi.LiftSuspensionRequest) (accountapi.AdminAccount, error) {
	moderator, err := s.requireModerator(ctx, req.ActorID)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	target, err := s.repo.FindAccountByID(ctx, req.AccountID.Int64())
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("find account by id: %w", err)
	}
	target.Reinstate()
	if err := s.repo.UpdateAccountStatus(ctx, target); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("update account status: %w", err)
	}
	s.audit(ctx, port.AuditEntry{
		Table:      "account",
		RecordID:   target.ID,
		ChangeType: "update",
		Code:       "account.reinstate",
		ChangedBy:  moderator.ID,
		Diff:       map[string]any{"status": string(target.Status)},
		Snapshot:   accountSnapshot(target),
	})
	return s.adminAccount(ctx, target)
}

// AdminCreateModerator is admin-only: a moderator decides disputes and takes listings
// down, so there is no self-service path to the role.
func (s *Service) AdminCreateModerator(ctx context.Context, req accountapi.CreateModeratorRequest) (accountapi.AdminAccount, error) {
	admin, err := s.requireAdmin(ctx, req.ActorID)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	acc, err := domain.NewAccount(domain.RoleModerator, req.Email, "", "", hash)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	profile, err := domain.NewProfile(req.Name, req.Country, req.Locale, req.Timezone)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	if err := s.repo.CreateAccount(ctx, &acc, &profile); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("create account: %w", err)
	}
	s.audit(ctx, port.AuditEntry{
		Table:      "account",
		RecordID:   acc.ID,
		ChangeType: "insert",
		Code:       "account.grant_moderator",
		ChangedBy:  admin.ID,
		Diff:       map[string]any{"role": string(acc.Role)},
		Snapshot:   accountSnapshot(acc),
	})
	return toAdminAccount(acc, profile, false), nil
}

// AdminRevokeModerator demotes to a plain user and drops the account's sessions — the
// role is carried in the row, and a signed-in moderator has to lose the console now
// rather than when their token expires. The account itself survives, since it may have
// traded.
func (s *Service) AdminRevokeModerator(ctx context.Context, req accountapi.RevokeModeratorRequest) error {
	admin, err := s.requireAdmin(ctx, req.ActorID)
	if err != nil {
		return err
	}
	target, err := s.repo.FindAccountByID(ctx, req.AccountID.Int64())
	if err != nil {
		return fmt.Errorf("find account by id: %w", err)
	}
	// The resource being addressed is the moderator, so an account that is not one is
	// simply not there.
	if target.Role != domain.RoleModerator {
		return domain.ErrAccountNotFound
	}
	if err := s.repo.UpdateAccountRole(ctx, target.ID, domain.RoleUser); err != nil {
		return fmt.Errorf("update account role: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, target.ID, ""); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	s.audit(ctx, port.AuditEntry{
		Table:      "account",
		RecordID:   target.ID,
		ChangeType: "update",
		Code:       "account.revoke_moderator",
		ChangedBy:  admin.ID,
		Diff:       map[string]any{"role": string(domain.RoleUser)},
		Snapshot:   accountSnapshot(target),
	})
	return nil
}

// --- helpers ---

// adminAccount builds the staff view of one account.
func (s *Service) adminAccount(ctx context.Context, a domain.Account) (accountapi.AdminAccount, error) {
	profile, err := s.repo.FindProfile(ctx, a.ID)
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("find profile: %w", err)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, a.ID)
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("check identity verified: %w", err)
	}
	return toAdminAccount(a, profile, verified), nil
}

func (s *Service) toAdminAccounts(ctx context.Context, rows []domain.Account) ([]accountapi.AdminAccount, error) {
	ids := make([]int64, 0, len(rows))
	for _, a := range rows {
		ids = append(ids, a.ID)
	}
	profiles, err := s.repo.FindProfiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("find profiles: %w", err)
	}
	verified, err := s.repo.LiveVerifiedDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("check identity verified: %w", err)
	}
	out := make([]accountapi.AdminAccount, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAdminAccount(a, profiles[a.ID], verified[a.ID]))
	}
	return out, nil
}

// audit appends to the module's audit log. It is best-effort and loud: the change it
// records has already been committed, so failing the request afterwards would report a
// suspension that did happen as one that did not — a dropped trail entry is a problem to
// alert on, not to hide behind a 500.
func (s *Service) audit(ctx context.Context, e port.AuditEntry) {
	if err := s.repo.InsertAuditLog(ctx, e); err != nil {
		s.log.Error("write audit log failed", "code", e.Code, "record_id", e.RecordID, "err", err)
	}
}

// accountSnapshot is the whole row as the audit log keeps it — identifiers included,
// password never.
func accountSnapshot(a domain.Account) map[string]any {
	return map[string]any{
		"id":                a.ID,
		"status":            string(a.Status),
		"role":              string(a.Role),
		"email":             a.Email,
		"phone":             a.Phone,
		"username":          a.Username,
		"email_verified":    a.EmailVerified,
		"suspended_until":   a.SuspendedUntil,
		"suspension_reason": a.SuspensionReason,
		"created_at":        a.CreatedAt,
	}
}

// auditIdentityVerdict records who decided a KYC case and how — the one fact the
// document row does not keep, since it holds the verdict but not its author.
func auditIdentityVerdict(d domain.IdentityDocument, byID int64) port.AuditEntry {
	return port.AuditEntry{
		Table:      "identity_document",
		RecordID:   d.ID,
		ChangeType: "update",
		Code:       "identity_document.verdict",
		ChangedBy:  byID,
		Diff:       map[string]any{"status": string(d.Status), "rejection_reason": d.RejectionReason, "expires_at": d.ExpiresAt},
		Snapshot: map[string]any{
			"id":               d.ID,
			"account_id":       d.AccountID,
			"doc_type":         string(d.DocType),
			"provider":         d.Provider,
			"status":           string(d.Status),
			"rejection_reason": d.RejectionReason,
			"verified_at":      d.VerifiedAt,
			"expires_at":       d.ExpiresAt,
		},
	}
}

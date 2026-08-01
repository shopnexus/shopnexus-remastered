package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common"
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
		Query:  domain.NormalizeIdentifier(req.Query),
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
	target, err := s.repo.Get(ctx, req.AccountID.Int64())
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("get account: %w", err)
	}
	target.Suspend(req.Reason, req.Until)
	// Save writes the trail for what Suspend recorded, in the transaction that suspends.
	if err := s.repo.Save(ctx, target, moderator.ID); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("save account: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, target.ID, ""); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("revoke sessions: %w", err)
	}
	return s.adminAccount(ctx, target)
}

// AdminLiftSuspension clears the reason and the deadline with the status: the row keeps
// only the suspension in force, and the one just lifted is in the audit log.
func (s *Service) AdminLiftSuspension(ctx context.Context, req accountapi.LiftSuspensionRequest) (accountapi.AdminAccount, error) {
	moderator, err := s.requireModerator(ctx, req.ActorID)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	target, err := s.repo.Get(ctx, req.AccountID.Int64())
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("get account: %w", err)
	}
	target.Reinstate()
	if err := s.repo.Save(ctx, target, moderator.ID); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("save account: %w", err)
	}
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
	profile, err := domain.NewProfile(req.Name, req.Country, req.Locale, req.Timezone)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	acc, err := domain.NewAccount(domain.RoleModerator, req.Email, "", "", hash, profile)
	if err != nil {
		return accountapi.AdminAccount{}, err
	}
	if err := s.repo.Create(ctx, acc); err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("create account: %w", err)
	}
	// An insert has no events to carry the trail, so this one is written on its own.
	s.audit(ctx, common.AuditEntry{
		Table:      "account",
		RecordID:   acc.ID,
		ChangeType: "insert",
		Code:       string(domain.RoleGranted.Code),
		ChangedBy:  &admin.ID,
		Diff:       domain.RoleChange{Role: acc.Role},
		Snapshot:   acc.Snapshot(),
	})
	return toAdminAccount(acc, false), nil
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
	target, err := s.repo.Get(ctx, req.AccountID.Int64())
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	// The resource being addressed is the moderator, so an account that is not one is
	// simply not there.
	if target.Role != domain.RoleModerator {
		return domain.ErrAccountNotFound
	}
	target.SetRole(domain.RoleUser)
	if err := s.repo.Save(ctx, target, admin.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, target.ID, ""); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

// --- helpers ---

// adminAccount builds the staff view of one account.
func (s *Service) adminAccount(ctx context.Context, a *domain.Account) (accountapi.AdminAccount, error) {
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, a.ID)
	if err != nil {
		return accountapi.AdminAccount{}, fmt.Errorf("check identity verified: %w", err)
	}
	return toAdminAccount(a, verified), nil
}

// toAdminAccounts resolves the identity flags for a whole page in one call — the display
// name already came back with the row.
func (s *Service) toAdminAccounts(ctx context.Context, rows []port.AccountSummary) ([]accountapi.AdminAccount, error) {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	verified, err := s.repo.LiveVerifiedDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("check identity verified: %w", err)
	}
	out := make([]accountapi.AdminAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, summaryToAdminAccount(r, verified[r.ID]))
	}
	return out, nil
}

// audit appends to the module's audit log. It is best-effort and loud: the change it
// records has already been committed, so failing the request afterwards would report a
// suspension that did happen as one that did not — a dropped trail entry is a problem to
// alert on, not to hide behind a 500.
func (s *Service) audit(ctx context.Context, e common.AuditEntry) {
	if err := s.repo.InsertAuditLog(ctx, e); err != nil {
		s.log.Error("write audit log failed", "code", e.Code, "record_id", e.RecordID, "err", err)
	}
}

// auditIdentityVerdict records who decided a KYC case and how — the one fact the
// document row does not keep, since it holds the verdict but not its author.
func auditIdentityVerdict(d domain.IdentityDocument, byID int64) common.AuditEntry {
	return common.AuditEntry{
		Table:      "identity_document",
		RecordID:   d.ID,
		ChangeType: "update",
		Code:       string(domain.IdentityVerdict.Code),
		ChangedBy:  &byID,
		Diff: domain.Verdict{
			Status:          d.Status,
			RejectionReason: d.RejectionReason,
			ExpiresAt:       d.ExpiresAt,
		},
		Snapshot: d.Snapshot(),
	}
}

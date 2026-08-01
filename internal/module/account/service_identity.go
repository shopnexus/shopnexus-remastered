package account

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/kyc"
	"shopnexus/internal/shared/id"
)

// StartIdentityVerification hands the scans to the vendor and stores what came back.
// What is kept is the vendor, its case reference and the verdict — never a document
// number and never a scan, so a leak of this table cannot impersonate anyone.
//
// The verdict may already be final: a vendor that reads the images answers in the same
// call, and the document is inserted decided. One that runs its own web flow answers
// pending and hands back a session URL for the caller to finish in.
func (s *Service) StartIdentityVerification(ctx context.Context, req accountapi.StartIdentityVerificationRequest) (accountapi.IdentityVerificationTicket, error) {
	accountID := req.ActorID.Int64()
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, accountID)
	if err != nil {
		return accountapi.IdentityVerificationTicket{}, fmt.Errorf("check identity verified: %w", err)
	}
	if verified {
		return accountapi.IdentityVerificationTicket{}, domain.ErrIdentityAlreadyVerified
	}
	profile, err := s.repo.FindProfile(ctx, accountID)
	if err != nil {
		return accountapi.IdentityVerificationTicket{}, fmt.Errorf("find profile: %w", err)
	}
	scans, err := s.scans(ctx, req)
	if err != nil {
		return accountapi.IdentityVerificationTicket{}, err
	}

	// The vendor is given the opaque id, not the database key: it is a third party.
	verdict, err := s.kyc.Check(ctx, kyc.CheckParams{
		AccountRef: req.ActorID.String(),
		DocType:    req.DocType,
		Locale:     profile.Locale,
		Front:      scans[req.FrontResourceID],
		Back:       scans[req.BackResourceID],
		Selfie:     scans[req.SelfieResourceID],
	})
	if err != nil {
		return accountapi.IdentityVerificationTicket{}, fmt.Errorf("run kyc check: %w", err)
	}

	doc, err := domain.NewIdentityDocument(accountID, domain.DocType(req.DocType), verdict.Provider, verdict.Ref)
	if err != nil {
		return accountapi.IdentityVerificationTicket{}, err
	}
	// The verdict goes through the same domain transitions a moderator's does, so the
	// rules about expiries and reasons cannot be bypassed by an automated check.
	switch verdict.Status {
	case kyc.StatusVerified:
		if err := doc.Verify(time.Now().UTC(), verdict.ExpiresAt); err != nil {
			return accountapi.IdentityVerificationTicket{}, err
		}
	case kyc.StatusRejected:
		if err := doc.Reject(verdict.RejectionReason); err != nil {
			return accountapi.IdentityVerificationTicket{}, err
		}
	}
	if err := s.repo.InsertIdentityDocument(ctx, &doc); err != nil {
		return accountapi.IdentityVerificationTicket{}, fmt.Errorf("insert identity document: %w", err)
	}
	ticket := accountapi.IdentityVerificationTicket{
		Document:               toIdentityDocument(doc),
		VendorSessionExpiresAt: verdict.SessionExpiresAt,
	}
	// A vendor that decides on the spot hands back no session to finish in.
	if verdict.SessionURL != "" {
		ticket.VendorSessionURL = &verdict.SessionURL
	}
	return ticket, nil
}

// scans resolves the uploaded images to the short-lived URLs the vendor reads them from.
// One batched call, and every id named has to come back: a check run against two of the
// three scans is not the check the caller asked for.
func (s *Service) scans(ctx context.Context, req accountapi.StartIdentityVerificationRequest) (map[id.ID[id.Resource]]kyc.Image, error) {
	wanted := []id.ID[id.Resource]{req.FrontResourceID, req.SelfieResourceID}
	if req.BackResourceID != 0 {
		wanted = append(wanted, req.BackResourceID)
	}
	keys := make([]int64, 0, len(wanted))
	for _, rid := range wanted {
		keys = append(keys, rid.Int64())
	}
	found, err := s.uploads.Resolve(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get scan resources: %w", err)
	}
	out := make(map[id.ID[id.Resource]]kyc.Image, len(found))
	for _, dto := range found {
		out[dto.ID] = kyc.Image{URL: dto.URL, Mime: dto.Mime}
	}
	for _, want := range wanted {
		// A resource that does not exist, was never confirmed, or has no fetch URL yet is
		// the same fact to the caller: the vendor cannot be shown this scan.
		if !out[want].Present() {
			s.log.Warn("scan is not available for verification", "resource_id", want.Int64())
			return nil, domain.ErrScanUnavailable
		}
	}
	return out, nil
}

// ListIdentityDocuments is the account's own history, expiries included: a passport runs
// out, so a payout gate has to look at more than the status.
func (s *Service) ListIdentityDocuments(ctx context.Context, req accountapi.ListIdentityDocumentsRequest) ([]accountapi.IdentityDocument, error) {
	rows, err := s.repo.ListIdentityDocuments(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list identity documents: %w", err)
	}
	out := make([]accountapi.IdentityDocument, 0, len(rows))
	for _, d := range rows {
		out = append(out, toIdentityDocument(d))
	}
	return out, nil
}

// AdminListIdentityDocuments is the review queue. The subject comes back beside the
// document, because a moderator cannot decide a case about an account they cannot see.
func (s *Service) AdminListIdentityDocuments(ctx context.Context, req accountapi.AdminListIdentityDocumentsRequest) (accountapi.Page[accountapi.AdminIdentityDocument], error) {
	if _, err := s.requireModerator(ctx, req.ActorID); err != nil {
		return accountapi.Page[accountapi.AdminIdentityDocument]{}, err
	}
	rows, total, err := s.repo.ListIdentityDocumentsByStatus(ctx,
		domain.IdentityStatus(req.Status), offsetOf(req.Page, req.Limit), req.Limit)
	if err != nil {
		return accountapi.Page[accountapi.AdminIdentityDocument]{}, fmt.Errorf("list identity document queue: %w", err)
	}

	accountIDs := make([]int64, 0, len(rows))
	for _, d := range rows {
		accountIDs = append(accountIDs, d.AccountID)
	}
	profiles, err := s.repo.FindProfiles(ctx, accountIDs)
	if err != nil {
		return accountapi.Page[accountapi.AdminIdentityDocument]{}, fmt.Errorf("find profiles: %w", err)
	}
	subjects := s.summariesByID(ctx, profiles)

	out := make([]accountapi.AdminIdentityDocument, 0, len(rows))
	for _, d := range rows {
		subject, ok := subjects[d.AccountID]
		if !ok {
			// A profile that did not come back still has to leave the case actionable, so
			// the entry names the account and nothing else.
			subject = accountapi.AccountSummary{ID: id.Of[id.Account](d.AccountID)}
		}
		out = append(out, accountapi.AdminIdentityDocument{
			Document: toIdentityDocument(d),
			Account:  subject,
		})
	}
	return accountapi.Page[accountapi.AdminIdentityDocument]{
		Data: out,
		Meta: accountapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// AdminRecordIdentityVerdict records the decision. The domain decides what a verdict
// requires — an expiry for a document type that has one, a reason for a rejection — and
// the repository only moves a row that is still pending, so two moderators deciding at
// once cannot overwrite each other.
func (s *Service) AdminRecordIdentityVerdict(ctx context.Context, req accountapi.IdentityVerdictRequest) (accountapi.IdentityDocument, error) {
	moderator, err := s.requireModerator(ctx, req.ActorID)
	if err != nil {
		return accountapi.IdentityDocument{}, err
	}
	doc, err := s.repo.FindIdentityDocument(ctx, req.DocumentID.Int64())
	if err != nil {
		return accountapi.IdentityDocument{}, fmt.Errorf("find identity document: %w", err)
	}

	if domain.IdentityStatus(req.Status) == domain.IdentityVerified {
		// Checked before the write as well as by the unique index, so the common case is a
		// clear 409 rather than a constraint violation translated back into one.
		live, err := s.repo.HasLiveVerifiedDocument(ctx, doc.AccountID)
		if err != nil {
			return accountapi.IdentityDocument{}, fmt.Errorf("check identity verified: %w", err)
		}
		if live {
			return accountapi.IdentityDocument{}, domain.ErrIdentityAlreadyVerified
		}
		if err := doc.Verify(time.Now().UTC(), req.ExpiresAt); err != nil {
			return accountapi.IdentityDocument{}, err
		}
	} else if err := doc.Reject(req.RejectionReason); err != nil {
		return accountapi.IdentityDocument{}, err
	}

	if err := s.repo.UpdateIdentityVerdict(ctx, doc); err != nil {
		return accountapi.IdentityDocument{}, fmt.Errorf("update identity document verdict: %w", err)
	}
	s.audit(ctx, auditIdentityVerdict(doc, moderator.ID))
	return toIdentityDocument(doc), nil
}

package trust

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// The queue's default slice: the unresolved reports, which is exactly the predicate the
// partial index covers.
var openStatuses = []string{domain.ReportStatusOpen, domain.ReportStatusReviewing}

// prefixFor maps a report's ref_type to the id prefix that decodes its ref_id. The
// polymorphic reference stays a string on the wire because its kind is only known at run
// time; these are the values report_ref_type accepts.
func prefixFor(refType string) (string, bool) {
	switch refType {
	case domain.ReportRefListing:
		return id.Prefix[id.Listing](), true
	case domain.ReportRefAccount:
		return id.Prefix[id.Account](), true
	case domain.ReportRefMessage:
		return id.Prefix[id.Message](), true
	case domain.ReportRefReview:
		return id.Prefix[id.Review](), true
	case domain.ReportRefReviewReply:
		return id.Prefix[id.ReviewReply](), true
	}
	return "", false
}

// SubmitReport files a complaint against a listing, an account, a message, a review or a
// reply. The target is checked against the module that owns it, so a typo'd id cannot fill
// the queue with reports about nothing.
func (s *Service) SubmitReport(ctx context.Context, req trustapi.SubmitReportRequest) (trustapi.Report, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Report{}, err
	}
	prefix, ok := prefixFor(req.RefType)
	if !ok {
		return trustapi.Report{}, errx.NewValidationError("unknown ref_type "+req.RefType,
			errx.Field{Field: "ref_type", Rule: "oneof", Message: "must be a known report target type"})
	}
	refID, err := id.ParseOpaque(prefix, req.RefID)
	if err != nil {
		return trustapi.Report{}, err
	}
	if err := s.requireTarget(ctx, req.ActorID, req.RefType, refID); err != nil {
		return trustapi.Report{}, err
	}
	r, err := domain.NewReport(req.ActorID.Int64(), req.RefType, refID, req.Reason, req.Detail)
	if err != nil {
		return trustapi.Report{}, err
	}
	if err := s.repo.InsertReport(ctx, &r); err != nil {
		return trustapi.Report{}, fmt.Errorf("insert report: %w", err)
	}
	return toAPIReport(r, prefix), nil
}

// ListMyReports is the reporter's own history. A reporter sees the status of what they filed
// but never who else reported the same target.
func (s *Service) ListMyReports(ctx context.Context, req trustapi.ListReportsRequest) (trustapi.ReportPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.ReportPage{}, err
	}
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.ReportPage{}, err
	}
	rows, err := s.repo.ListReports(ctx, port.ReportFilter{
		ReporterID: req.ActorID.Int64(), Statuses: statusFilter(req.Status), Cursor: cursor,
	})
	if err != nil {
		return trustapi.ReportPage{}, fmt.Errorf("list reports: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, func(r domain.Report) time.Time { return r.CreatedAt })
	data := make([]trustapi.Report, 0, len(rows))
	for _, row := range rows {
		prefix, _ := prefixFor(row.RefType)
		data = append(data, toAPIReport(row, prefix))
	}
	return trustapi.ReportPage{Data: data, Meta: meta}, nil
}

// AdminListReports is the moderator queue, oldest first — the order it is worked. It defaults
// to the unresolved slice rather than the whole table.
func (s *Service) AdminListReports(ctx context.Context, req trustapi.AdminListReportsRequest) (trustapi.AdminReportPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.AdminReportPage{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.AdminReportPage{}, err
	}
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.AdminReportPage{}, err
	}
	statuses := openStatuses
	if req.Status != "" {
		statuses = []string{req.Status}
	}
	rows, err := s.repo.ListReports(ctx, port.ReportFilter{
		Statuses: statuses, RefType: req.RefType, Reason: req.Reason, Cursor: cursor,
	})
	if err != nil {
		return trustapi.AdminReportPage{}, fmt.Errorf("list reports: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, func(r domain.Report) time.Time { return r.CreatedAt })
	data := make([]trustapi.AdminReport, 0, len(rows))
	for _, row := range rows {
		// The queue is a work list: it carries the reporter and the pattern, not the
		// reported content, which the single-report read fetches.
		entry, err := s.adminView(ctx, req.ActorID, row, false)
		if err != nil {
			return trustapi.AdminReportPage{}, err
		}
		data = append(data, entry)
	}
	return trustapi.AdminReportPage{Data: data, Meta: meta}, nil
}

// AdminGetReport reads one case with the reported content beside it, and how many others
// named the same target: a moderator decides on the pattern rather than one complaint.
func (s *Service) AdminGetReport(ctx context.Context, req trustapi.ReportRequest) (trustapi.AdminReport, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.AdminReport{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.AdminReport{}, err
	}
	r, err := s.repo.FindReport(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.AdminReport{}, fmt.Errorf("find report: %w", err)
	}
	return s.adminView(ctx, req.ActorID, r, true)
}

// AdminClaimReport takes an open case for review, so two moderators do not work the same one.
func (s *Service) AdminClaimReport(ctx context.Context, req trustapi.ReportRequest) (trustapi.Report, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Report{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.Report{}, err
	}
	r, err := s.repo.FindReport(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Report{}, fmt.Errorf("find report: %w", err)
	}
	if err := r.Claim(); err != nil {
		return trustapi.Report{}, err
	}
	// Guarded by the status it moves from, so two moderators claiming at once means one wins.
	if err := s.repo.SaveReport(ctx, r, []string{domain.ReportStatusOpen}); err != nil {
		return trustapi.Report{}, domain.ErrReportNotClaimable
	}
	prefix, _ := prefixFor(r.RefType)
	return toAPIReport(r, prefix), nil
}

// AdminResolveReport records the verdict and what was done about it. Recording is all it
// does: taking a listing down and suspending an account are calls to the modules that own
// them, so the decision and its effects each stay where they can be audited.
func (s *Service) AdminResolveReport(ctx context.Context, req trustapi.ResolveReportRequest) (trustapi.Report, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Report{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.Report{}, err
	}
	r, err := s.repo.FindReport(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Report{}, fmt.Errorf("find report: %w", err)
	}
	if err := r.Resolve(req.ActorID.Int64(), req.Status, req.ActionTaken, req.Note); err != nil {
		return trustapi.Report{}, err
	}
	if err := s.repo.SaveReport(ctx, r, openStatuses); err != nil {
		return trustapi.Report{}, err
	}
	prefix, _ := prefixFor(r.RefType)
	return toAPIReport(r, prefix), nil
}

// requireTarget refuses a report against something that does not exist. Each kind is asked of
// the module that owns it — a resource id only resolves inside its module, and so does
// everything else.
func (s *Service) requireTarget(ctx context.Context, actorID id.ID[id.Account], refType string, refID int64) error {
	var err error
	switch refType {
	case domain.ReportRefListing:
		_, err = s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
			ID: id.Of[id.Listing](refID), ViewerID: actorID,
		})
	case domain.ReportRefAccount:
		_, err = s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
			ID: id.Of[id.Account](refID),
		})
	case domain.ReportRefMessage:
		_, err = s.chat.GetMessage(ctx, chatapi.GetMessageRequest{
			ActorID: actorID, ID: id.Of[id.Message](refID),
		})
	case domain.ReportRefReview:
		_, err = s.repo.FindReview(ctx, refID)
	case domain.ReportRefReviewReply:
		_, err = s.repo.FindReply(ctx, refID)
	}
	if err != nil {
		return domain.ErrReportTargetNotFound
	}
	return nil
}

// target is the reported content, fetched from the module that owns it. Best-effort: a
// listing already taken down leaves the queue entry readable, because the report is still a
// decision somebody has to record.
func (s *Service) target(ctx context.Context, actorID id.ID[id.Account], r domain.Report) map[string]any {
	switch r.RefType {
	case domain.ReportRefListing:
		listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
			ID: id.Of[id.Listing](r.RefID), ViewerID: actorID,
		})
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": listing.ID, "name": listing.Name, "status": listing.Status,
			"seller": listing.Seller,
		}
	case domain.ReportRefAccount:
		account, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
			ID: id.Of[id.Account](r.RefID),
		})
		if err != nil {
			return nil
		}
		return map[string]any{"id": account.ID, "name": account.Name, "created_at": account.CreatedAt}
	case domain.ReportRefMessage:
		message, err := s.chat.GetMessage(ctx, chatapi.GetMessageRequest{
			ActorID: actorID, ID: id.Of[id.Message](r.RefID),
		})
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": message.ID, "sender_id": message.SenderID, "body": message.Body,
			"created_at": message.CreatedAt,
		}
	case domain.ReportRefReview:
		review, err := s.repo.FindReview(ctx, r.RefID)
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": id.Of[id.Review](review.ID), "listing_id": id.Of[id.Listing](review.ListingID),
			"author_id": id.Of[id.Account](review.AuthorID), "rating": review.Rating,
			"body": review.Body, "created_at": review.CreatedAt,
		}
	case domain.ReportRefReviewReply:
		reply, err := s.repo.FindReply(ctx, r.RefID)
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": id.Of[id.ReviewReply](reply.ID), "review_id": id.Of[id.Review](reply.ReviewID),
			"author_id": id.Of[id.Account](reply.AuthorID), "body": reply.Body,
			"created_at": reply.CreatedAt,
		}
	}
	return nil
}

func (s *Service) adminView(ctx context.Context, actorID id.ID[id.Account], r domain.Report,
	withTarget bool) (trustapi.AdminReport, error) {
	reporter, err := s.summary(ctx, r.ReporterID)
	if err != nil {
		return trustapi.AdminReport{}, err
	}
	count, err := s.repo.CountOpenAgainst(ctx, r.RefType, r.RefID)
	if err != nil {
		return trustapi.AdminReport{}, fmt.Errorf("count open reports: %w", err)
	}
	prefix, _ := prefixFor(r.RefType)
	entry := trustapi.AdminReport{
		Report:                   toAPIReport(r, prefix),
		Reporter:                 reporter,
		OpenReportsAgainstTarget: count,
	}
	if r.ResolvedByID != nil {
		resolver, err := s.summary(ctx, *r.ResolvedByID)
		if err != nil {
			return trustapi.AdminReport{}, err
		}
		entry.ResolvedBy = &resolver
	}
	if withTarget {
		entry.Target = s.target(ctx, actorID, r)
	}
	return entry, nil
}

func toAPIReport(r domain.Report, prefix string) trustapi.Report {
	return trustapi.Report{
		ID:             id.Of[id.Report](r.ID),
		RefType:        r.RefType,
		RefID:          id.FormatOpaque(prefix, r.RefID),
		Reason:         r.Reason,
		Detail:         r.Detail,
		Status:         r.Status,
		ActionTaken:    r.ActionTaken,
		ResolvedAt:     r.ResolvedAt,
		ResolutionNote: r.ResolutionNote,
		CreatedAt:      r.CreatedAt,
	}
}

// statusFilter turns an optional single status into the set the repository takes. Absent
// means every status, which is what a reporter's own history shows.
func statusFilter(status string) []string {
	if status == "" {
		return nil
	}
	return []string{status}
}

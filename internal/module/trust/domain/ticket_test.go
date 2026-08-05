package domain_test

import (
	"errors"
	"testing"

	"shopnexus/internal/module/trust/domain"
)

// The ref/reason matrix is what `kind` decides, and it is the domain's rule rather than the
// service's: a report names a target and says what is wrong with it, a refund dispute names the
// refund and nothing more, and a feature request names nothing at all.
func TestNewTicket_RefAndReasonFollowTheKind(t *testing.T) {
	listing, refund := domain.RefListing, domain.RefRefund
	target, reason := int64(20), "counterfeit"
	zero := int64(0)

	cases := []struct {
		name    string
		kind    string
		refType *string
		refID   *int64
		reason  *string
		want    error
	}{
		{"a report names its target and its grounds", domain.KindReportListing, &listing, &target, &reason, nil},
		{"a report with no grounds", domain.KindReportListing, &listing, &target, nil, domain.ErrTicketReasonMismatch},
		{"a report with no target", domain.KindReportListing, nil, nil, &reason, domain.ErrTicketRefRequired},
		{"a report whose target is another kind of thing", domain.KindReportListing, &refund, &target, &reason, domain.ErrTicketRefRequired},
		{"a target id of zero is no target", domain.KindReportListing, &listing, &zero, &reason, domain.ErrTicketRefRequired},
		{"a refund dispute names the refund, with no grounds", domain.KindRefundDispute, &refund, &target, nil, nil},
		{"a refund dispute with grounds", domain.KindRefundDispute, &refund, &target, &reason, domain.ErrTicketReasonMismatch},
		{"a refund dispute with no refund", domain.KindRefundDispute, nil, nil, nil, domain.ErrTicketRefRequired},
		{"a feature request is about nothing", domain.KindFeatureRequest, nil, nil, nil, nil},
		{"a feature request with a target", domain.KindFeatureRequest, &listing, &target, nil, domain.ErrTicketRefUnexpected},
		{"a feature request with grounds", domain.KindFeatureRequest, nil, nil, &reason, domain.ErrTicketReasonMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := domain.NewTicket(7, c.kind, "Hàng giả", c.refType, c.refID, c.reason)
			if !errors.Is(err, c.want) {
				t.Fatalf("NewTicket = %v, want %v", err, c.want)
			}
			if c.want != nil {
				return
			}
			if got.Status != domain.StatusOpen || got.RequesterID != 7 {
				t.Fatalf("ticket = %+v, want an open ticket for its requester", got)
			}
		})
	}
}

// A kind absent from the table is about nothing, and only the report kinds carry grounds. Both are
// derived from the kind, so a kind added to the enum and nowhere else is refused rather than quietly
// treated as a report.
func TestTicketKind_WhatItIsAboutAndWhetherItHasGrounds(t *testing.T) {
	for kind, want := range map[string]string{
		domain.KindReportListing:     domain.RefListing,
		domain.KindReportReviewReply: domain.RefReviewReply,
		domain.KindRefundDispute:     domain.RefRefund,
		domain.KindOrderIssue:        domain.RefOrder,
		domain.KindPayment:           "",
		domain.KindFeatureRequest:    "",
		domain.KindOther:             "",
		"report-something-new":       "",
	} {
		if got := domain.RefKindOf(kind); got != want {
			t.Errorf("RefKindOf(%q) = %q, want %q", kind, got, want)
		}
	}
	for kind, want := range map[string]bool{
		domain.KindReportListing:     true,
		domain.KindReportReviewReply: true,
		domain.KindRefundDispute:     false,
		domain.KindPayment:           false,
		// A kind that only looks like a report is not one: the vocabulary is the constants, not a
		// prefix somebody happened to spell the same way.
		"report-something-new": false,
	} {
		if got := domain.Reported(kind); got != want {
			t.Errorf("Reported(%q) = %v, want %v", kind, got, want)
		}
	}
}

// Resolving is once, and the action has to be one this module defined. The route's own vocabulary is
// narrower — the two refund-* values are order's verdict to record — so this is the floor under it
// rather than the same check twice.
func TestTicketResolve_OnceAndOnlyWithAKnownAction(t *testing.T) {
	refund := domain.RefRefund
	target := int64(55)
	one, err := domain.NewTicket(7, domain.KindRefundDispute, "Hàng không đúng mô tả", &refund, &target, nil)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := one.Resolve(9, "listing-deleted-maybe", ""); !errors.Is(err, domain.ErrTicketActionInvalid) {
		t.Fatalf("Resolve with an invented action = %v, want ErrTicketActionInvalid", err)
	}
	if err := one.Resolve(9, domain.ActionRefundGranted, "ảnh mở hộp rõ ràng"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !one.Resolved() || one.ResolvedAt == nil || one.ResolvedByID == nil || *one.ResolvedByID != 9 ||
		one.ResolutionNote == nil {
		t.Fatalf("ticket = %+v, want the verdict and its author recorded", one)
	}
	if err := one.Resolve(9, domain.ActionNone, ""); !errors.Is(err, domain.ErrTicketResolved) {
		t.Fatalf("second Resolve = %v, want ErrTicketResolved", err)
	}
}

// Claiming is from `open` only: a claimed ticket is somebody's and a resolved one is finished.
func TestTicketClaim_FromOpenOnly(t *testing.T) {
	one, err := domain.NewTicket(7, domain.KindFeatureRequest, "Lọc theo tỉnh", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := one.Claim(9); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if one.Status != domain.StatusReviewing || one.AssigneeID == nil || *one.AssigneeID != 9 {
		t.Fatalf("ticket = %+v, want it claimed by the moderator", one)
	}
	if err := one.Claim(10); !errors.Is(err, domain.ErrTicketNotClaimable) {
		t.Fatalf("second Claim = %v, want ErrTicketNotClaimable", err)
	}
}

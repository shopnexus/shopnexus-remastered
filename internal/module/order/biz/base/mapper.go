package base

import (
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
)

func MapRefund(r orderdb.OrderRefund) ordermodel.Refund {
	return ordermodel.Refund{
		ID:                       r.ID,
		AccountID:                r.AccountID,
		OrderID:                  r.OrderID,
		Reason:                   r.Reason,
		Attachments:              r.Attachments,
		DateCreated:              r.DateCreated,
		Status:                   ordermodel.RefundStatus(r.Status),
		ReturnTransportID:        r.ReturnTransportID,
		DateReceivedBySeller:     r.DateReceivedBySeller,
		ReviewDeadline:           r.ReviewDeadline,
		SellerDecisionAt:         r.SellerDecisionAt,
		ReturnToBuyerTransportID: r.ReturnToBuyerTransportID,
		RejectionReason:          r.RejectionReason,
		RefundTxID:               r.RefundTxID,
	}
}

func MapRefundDispute(d orderdb.OrderRefundDispute) ordermodel.RefundDispute {
	return ordermodel.RefundDispute{
		ID:             d.ID,
		RefundID:       d.RefundID,
		AccountID:      d.AccountID,
		Reason:         d.Reason,
		Attachments:    d.Attachments,
		Status:         ordermodel.DisputeStatus(d.Status),
		DateCreated:    d.DateCreated,
		ResolvedByID:   d.ResolvedByID,
		DateResolved:   d.DateResolved,
		ResolutionNote: d.ResolutionNote,
	}
}

package orderbiz

import (
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
)

func mapTransport(t orderdb.OrderTransport) ordermodel.Transport {
	var status orderdb.OrderStatus
	if t.Status.Valid {
		status = t.Status.OrderStatus
	}
	return ordermodel.Transport{
		ID:          t.ID,
		OptionID:    t.Option,
		Status:      ordermodel.Status(status),
		Data:        t.Data,
		DateCreated: t.DateCreated,
	}
}

// mapPaymentSession converts an sqlc OrderPaymentSession row to the domain model.
func mapPaymentSession(s orderdb.OrderPaymentSession) ordermodel.PaymentSession {
	return ordermodel.PaymentSession{
		ID:          s.ID,
		Kind:        s.Kind,
		Status:      ordermodel.Status(s.Status),
		FromID:      s.FromID,
		ToID:        s.ToID,
		Note:        s.Note,
		Currency:    s.Currency,
		TotalAmount: s.TotalAmount,
		FxSnapshot:  s.FxSnapshot,
		Data:        s.Data,
		DateCreated: s.DateCreated,
		DatePaid:    s.DatePaid,
		DateExpired: s.DateExpired,
	}
}

func mapOrderItem(it orderdb.OrderItem) ordermodel.OrderItem {
	return ordermodel.OrderItem{
		ID:               it.ID,
		OrderID:          it.OrderID,
		AccountID:        it.AccountID,
		SellerID:         it.SellerID,
		SkuID:            it.SkuID,
		SpuID:            it.SpuID,
		SkuName:          it.SkuName,
		Address:          it.Address,
		Note:             it.Note,
		SerialIDs:        it.SerialIds,
		Quantity:         it.Quantity,
		TransportOption:  it.TransportOption,
		SubtotalAmount:   it.SubtotalAmount,
		TotalAmount:      it.TotalAmount,
		SourceCurrency:   it.SourceCurrency,
		PaymentSessionID: it.PaymentSessionID,
		DateCreated:      it.DateCreated,
		DateCancelled:    it.DateCancelled,
		CancelledByID:    it.CancelledByID,
	}
}

func mapOrder(o orderdb.OrderOrder) ordermodel.Order {
	return ordermodel.Order{
		ID:               o.ID,
		BuyerID:          o.BuyerID,
		SellerID:         o.SellerID,
		TransportID:      o.TransportID,
		Address:          o.Address,
		DateCreated:      o.DateCreated,
		ConfirmedByID:    o.ConfirmedByID,
		ConfirmSessionID: o.ConfirmSessionID,
		Note:             o.Note,
	}
}

func mapRefund(r orderdb.OrderRefund) ordermodel.Refund {
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

func mapRefundDispute(d orderdb.OrderRefundDispute) ordermodel.RefundDispute {
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

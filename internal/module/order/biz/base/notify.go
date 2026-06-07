package base

import (
	"context"
	"encoding/json"
	"fmt"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
)

// NotifyOrder pushes an in-app notification tagged with the order ID.
func (b *Base) NotifyOrder(
	ctx context.Context,
	accountID, orderID uuid.UUID,
	notiType accountmodel.NotificationType,
	title, content string,
) error {
	meta, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
	return b.Notify(ctx, accountbiz.CreateNotificationParams{
		AccountID: accountID,
		Type:      notiType,
		Channel:   accountmodel.ChannelInApp,
		Title:     title,
		Content:   content,
		Metadata:  meta,
	})
}

// NotifyDispute notifies both dispute parties about an outcome.
func (b *Base) NotifyDispute(
	ctx context.Context,
	buyerID, sellerID uuid.UUID,
	dispute orderdb.OrderRefundDispute,
	title, content string,
) error {
	meta, _ := json.Marshal(map[string]string{
		"refund_id":  dispute.RefundID.String(),
		"dispute_id": dispute.ID.String(),
		"outcome":    string(dispute.Status),
	})
	for _, accountID := range []uuid.UUID{buyerID, sellerID} {
		if err := b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: accountID,
			Type:      accountmodel.NotiDisputeOpened,
			Channel:   accountmodel.ChannelInApp,
			Title:     title,
			Content:   content,
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify dispute outcome: %w", err)
		}
	}
	return nil
}

// NotifyRefund pushes an in-app notification about a refund transition.
// Shared by the refund handlers and the fulfillment workflow.
func (b *Base) NotifyRefund(
	ctx context.Context,
	accountID uuid.UUID,
	notiType accountmodel.NotificationType,
	title, content string,
	refund orderdb.OrderRefund,
) error {
	meta, _ := json.Marshal(map[string]string{
		"refund_id": refund.ID.String(),
		"order_id":  refund.OrderID.String(),
	})
	return b.Notify(ctx, accountbiz.CreateNotificationParams{
		AccountID: accountID,
		Type:      notiType,
		Channel:   accountmodel.ChannelInApp,
		Title:     title,
		Content:   content,
		Metadata:  meta,
	})
}

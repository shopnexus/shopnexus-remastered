package accountbiz

import (
	"encoding/json"
	"fmt"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/paginate"
)

type ListNotificationParams struct {
	Account accountmodel.AuthenticatedAccount
	paginate.Params
}

// ListNotification returns paginated notifications for the authenticated account.
func (b *AccountHandler) ListNotification(
	ctx restate.Context,
	params ListNotificationParams,
) (paginate.PaginateResult[accountdb.AccountNotification], error) {
	var zero paginate.PaginateResult[accountdb.AccountNotification]
	params.Params = params.Constrain()

	rows, err := b.storage.Querier().ListNotificationByAccount(ctx, accountdb.ListNotificationByAccountParams{
		AccountID: params.Account.ID,
		Limit:     null.Int32From(params.Limit.Int32),
		Offset:    params.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("list notifications: %w", err)
	}

	var total null.Int64
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	return paginate.PaginateResult[accountdb.AccountNotification]{
		PageParams: params.Params,
		Total:      total,
		Data: lo.Map(rows, func(r accountdb.ListNotificationByAccountRow, _ int) accountdb.AccountNotification {
			return r.AccountNotification
		}),
	}, nil
}

type CountUnreadParams struct {
	AccountID uuid.UUID
}

// CountUnread returns the number of unread notifications for the given account.
func (b *AccountHandler) CountUnread(ctx restate.Context, params CountUnreadParams) (int64, error) {
	count, err := b.storage.Querier().CountUnreadByAccount(ctx, params.AccountID)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

type MarkReadParams struct {
	Account accountmodel.AuthenticatedAccount
	IDs     []int64 `validate:"required,min=1"`
}

// MarkRead marks the specified notification IDs as read.
func (b *AccountHandler) MarkRead(ctx restate.Context, params MarkReadParams) error {
	if err := b.storage.Querier().MarkNotificationRead(ctx, accountdb.MarkNotificationReadParams{
		ID:        params.IDs,
		AccountID: params.Account.ID,
	}); err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}

	return nil
}

type MarkAllReadParams struct {
	AccountID uuid.UUID
}

// MarkAllRead marks all unread notifications as read for the given account.
func (b *AccountHandler) MarkAllRead(ctx restate.Context, params MarkAllReadParams) error {
	if err := b.storage.Querier().MarkAllNotificationRead(ctx, params.AccountID); err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}

	return nil
}

type CreateNotificationParams struct {
	AccountID uuid.UUID
	Type      accountmodel.NotificationType
	Channel   accountmodel.NotificationChannel
	Title     string
	Content   string
	Metadata  json.RawMessage
}

// CreateNotification creates a new notification for the given account.
func (b *AccountHandler) CreateNotification(
	ctx restate.Context,
	params CreateNotificationParams,
) (accountdb.AccountNotification, error) {
	noti, err := b.storage.Querier().CreateDefaultNotification(ctx, accountdb.CreateDefaultNotificationParams{
		AccountID: params.AccountID,
		Type:      string(params.Type),
		Channel:   string(params.Channel),
		Title:     params.Title,
		Content:   params.Content,
		Metadata:  params.Metadata,
	})
	if err != nil {
		return accountdb.AccountNotification{}, fmt.Errorf("create notification: %w", err)
	}

	// Push real-time notification to SSE clients
	restate.ServiceSend(ctx, "Common", "PushEvent").Send(commonbiz.PushEventParams{
		AccountID: params.AccountID,
		Type:      commonbiz.SSENotification,
		Data:      noti,
	})

	return noti, nil
}

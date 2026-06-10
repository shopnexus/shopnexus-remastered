package accountbiz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

type ListNotificationParams struct {
	paginate.Params

	Account accountmodel.AuthenticatedAccount
}

// ListNotification returns paginated notifications for the authenticated account.
func (b *AccountHandler) ListNotification(
	ctx context.Context,
	params ListNotificationParams,
) (paginate.PaginateResult[accountdb.AccountNotification], error) {
	var zero paginate.PaginateResult[accountdb.AccountNotification]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list notification params: %w", err)
	}

	rows, err := b.storage.Querier().ListNotificationByAccount(ctx, accountdb.ListNotificationByAccountParams{
		AccountID: params.Account.ID,
		Limit:     null.Int32From(params.Limit.Int32),
		Offset:    params.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db list notification by account: %w", err)
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
func (b *AccountHandler) CountUnread(ctx context.Context, params CountUnreadParams) (int64, error) {
	if err := validator.Validate(params); err != nil {
		return 0, fmt.Errorf("validate count unread params: %w", err)
	}
	count, err := b.storage.Querier().CountUnreadByAccount(ctx, params.AccountID)
	if err != nil {
		return 0, fmt.Errorf("db count unread by account: %w", err)
	}
	return count, nil
}

type MarkReadParams struct {
	Account accountmodel.AuthenticatedAccount
	IDs     []int64 `validate:"required,min=1"`
}

// MarkRead marks the specified notification IDs as read.
func (b *AccountHandler) MarkRead(ctx context.Context, params MarkReadParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate mark read params: %w", err)
	}
	if err := b.storage.Querier().MarkNotificationRead(ctx, accountdb.MarkNotificationReadParams{
		ID:        params.IDs,
		AccountID: params.Account.ID,
	}); err != nil {
		return fmt.Errorf("db mark notification read: %w", err)
	}

	return nil
}

type MarkAllReadParams struct {
	AccountID uuid.UUID
}

// MarkAllRead marks all unread notifications as read for the given account.
func (b *AccountHandler) MarkAllRead(ctx context.Context, params MarkAllReadParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate mark all read params: %w", err)
	}
	if err := b.storage.Querier().MarkAllNotificationRead(ctx, params.AccountID); err != nil {
		return fmt.Errorf("db mark all notification read: %w", err)
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
	ctx context.Context,
	params CreateNotificationParams,
) (accountdb.AccountNotification, error) {
	if err := validator.Validate(params); err != nil {
		return accountdb.AccountNotification{}, fmt.Errorf("validate create notification params: %w", err)
	}
	noti, err := b.storage.Querier().CreateDefaultNotification(ctx, accountdb.CreateDefaultNotificationParams{
		AccountID: params.AccountID,
		Type:      string(params.Type),
		Channel:   string(params.Channel),
		Title:     params.Title,
		Content:   params.Content,
		Metadata:  params.Metadata,
	})
	if err != nil {
		return accountdb.AccountNotification{}, fmt.Errorf("db create default notification: %w", err)
	}

	// Push real-time notification to SSE clients
	if err = b.common.Guaranteed().Send().PushEvent(ctx, commonbiz.PushEventParams{
		AccountID: params.AccountID,
		Type:      commonbiz.SSENotification,
		Data:      noti,
	}); err != nil {
		return accountdb.AccountNotification{}, fmt.Errorf("push notification event: %w", err)
	}

	return noti, nil
}

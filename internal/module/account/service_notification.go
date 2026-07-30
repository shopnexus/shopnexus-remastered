package account

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// ListNotifications reads the feed newest first. One extra row is asked for beyond the
// page: that is how the next cursor is known to exist without a second query, and it
// avoids handing back a cursor that leads to an empty page.
func (s *Service) ListNotifications(ctx context.Context, req accountapi.ListNotificationsRequest) (accountapi.Cursor[accountapi.Notification], error) {
	var before time.Time
	if req.Cursor != "" {
		t, err := decodeCursor(req.Cursor)
		if err != nil {
			return accountapi.Cursor[accountapi.Notification]{}, err
		}
		before = t
	}
	rows, err := s.repo.ListNotifications(ctx, port.NotificationQuery{
		AccountID:  req.ActorID.Int64(),
		Category:   domain.Category(req.Category),
		UnreadOnly: req.Unread != nil && *req.Unread,
		Before:     before,
		Limit:      req.Limit + 1,
	})
	if err != nil {
		return accountapi.Cursor[accountapi.Notification]{}, fmt.Errorf("list notifications: %w", err)
	}

	var next *string
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		c := encodeCursor(rows[len(rows)-1].CreatedAt)
		next = &c
	}
	out := make([]accountapi.Notification, 0, len(rows))
	for _, n := range rows {
		out = append(out, accountapi.Notification{
			Category:  string(n.Category),
			Title:     n.Title,
			Payload:   n.Payload,
			ReadAt:    n.ReadAt,
			CreatedAt: n.CreatedAt,
		})
	}
	return accountapi.Cursor[accountapi.Notification]{Data: out, NextCursor: next}, nil
}

func (s *Service) GetUnreadCount(ctx context.Context, req accountapi.GetUnreadCountRequest) (accountapi.UnreadCount, error) {
	n, err := s.repo.CountUnreadNotifications(ctx, req.ActorID.Int64())
	if err != nil {
		return accountapi.UnreadCount{}, fmt.Errorf("count unread notifications: %w", err)
	}
	return accountapi.UnreadCount{Unread: n}, nil
}

// MarkNotificationsRead answers with the badge that follows, so the client does not have
// to ask for it in a second call it would make every single time.
func (s *Service) MarkNotificationsRead(ctx context.Context, req accountapi.MarkNotificationsReadRequest) (accountapi.UnreadCount, error) {
	if err := s.repo.MarkNotificationsRead(ctx, req.ActorID.Int64(), req.Before); err != nil {
		return accountapi.UnreadCount{}, fmt.Errorf("mark notifications read: %w", err)
	}
	return s.GetUnreadCount(ctx, accountapi.GetUnreadCountRequest{ActorID: req.ActorID})
}

// GetNotificationPreferences resolves the sparse stored rows against the domain's
// defaults and returns the whole matrix, so a client never has to know the defaults.
func (s *Service) GetNotificationPreferences(ctx context.Context, req accountapi.GetNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	stored, err := s.repo.ListPreferences(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	return toAPIPreferences(domain.ResolvePreferences(stored)), nil
}

func (s *Service) UpdateNotificationPreferences(ctx context.Context, req accountapi.UpdateNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	accountID := req.ActorID.Int64()
	want := make([]domain.Preference, 0, len(req.Items))
	for _, item := range req.Items {
		want = append(want, domain.Preference{
			AccountID: accountID,
			Category:  domain.Category(item.Category),
			Channel:   domain.Channel(item.Channel),
			IsEnabled: item.IsEnabled != nil && *item.IsEnabled,
		})
	}
	// The domain decides which of these becomes a row and which deletes one, because
	// "equal to the default" is a domain fact that changes without a migration.
	store, remove := domain.SplitPreferences(want)
	if err := s.repo.SavePreferences(ctx, accountID, store, remove); err != nil {
		return nil, fmt.Errorf("save notification preferences: %w", err)
	}
	return s.GetNotificationPreferences(ctx, accountapi.GetNotificationPreferencesRequest{ActorID: req.ActorID})
}

func toAPIPreferences(rows []domain.EffectivePreference) []accountapi.NotificationPreference {
	out := make([]accountapi.NotificationPreference, 0, len(rows))
	for _, p := range rows {
		out = append(out, accountapi.NotificationPreference{
			Category:  string(p.Category),
			Channel:   string(p.Channel),
			IsEnabled: p.IsEnabled,
			IsDefault: p.IsDefault,
		})
	}
	return out
}

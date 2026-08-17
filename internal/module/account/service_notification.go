package account

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/validation"
)

// ListNotifications reads the feed newest first. One extra row is asked for beyond the
// page: that is how the next cursor is known to exist without a second query, and it
// avoids handing back a cursor that leads to an empty page.
func (s *Service) ListNotifications(ctx context.Context, req accountapi.ListNotificationsRequest) (accountapi.Cursor[accountapi.Notification], error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Cursor[accountapi.Notification]{}, err
	}
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
		out = append(out, toAPINotification(n))
	}
	return accountapi.Cursor[accountapi.Notification]{Data: out, NextCursor: next}, nil
}

func (s *Service) GetUnreadCount(ctx context.Context, req accountapi.GetUnreadCountRequest) (accountapi.UnreadCount, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.UnreadCount{}, err
	}
	n, err := s.repo.CountUnreadNotifications(ctx, req.ActorID.Int64())
	if err != nil {
		return accountapi.UnreadCount{}, fmt.Errorf("count unread notifications: %w", err)
	}
	return accountapi.UnreadCount{Unread: n}, nil
}

// MarkNotificationsRead answers with the badge that follows, so the client does not have
// to ask for it in a second call it would make every single time.
func (s *Service) MarkNotificationsRead(ctx context.Context, req accountapi.MarkNotificationsReadRequest) (accountapi.UnreadCount, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.UnreadCount{}, err
	}
	if err := s.repo.MarkNotificationsRead(ctx, req.ActorID.Int64(), req.Before); err != nil {
		return accountapi.UnreadCount{}, fmt.Errorf("mark notifications read: %w", err)
	}
	return s.GetUnreadCount(ctx, accountapi.GetUnreadCountRequest{ActorID: req.ActorID})
}

// GetNotificationPreferences resolves the sparse stored rows against the domain's
// defaults and returns the whole matrix, so a client never has to know the defaults.
func (s *Service) GetNotificationPreferences(ctx context.Context, req accountapi.GetNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	stored, err := s.repo.ListPreferences(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	return toAPIPreferences(domain.ResolvePreferences(stored)), nil
}

func (s *Service) UpdateNotificationPreferences(ctx context.Context, req accountapi.UpdateNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
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

// CreateNotification tells one account one thing, over the channels they left on.
//
// The feed row is the one this module owns a table for; the mail is sent through the notify
// provider and nothing records that it went, because a row standing for "we tried to email
// you" would be a second, weaker definition of the feed. Push and SMS are still a workflow's
// problem.
//
// The two channels are decided independently. Turning the feed off is not a way to stop the
// mail, and it used to be: the in-app preference returned early, in front of everything.
func (s *Service) CreateNotification(ctx context.Context, req accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Notification{}, err
	}
	accountID := req.AccountID.Int64()
	category := domain.Category(req.Category)

	// Validated before the preference lookup: an unknown category must answer
	// ErrNotificationInvalid, not silently read as "off" the way DefaultPreference
	// treats any pair it does not recognise.
	n, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID: accountID,
		Category:  category,
		Title:     req.Title,
		Payload:   req.Payload,
	})
	if err != nil {
		return accountapi.Notification{}, err
	}

	stored, err := s.repo.ListPreferences(ctx, accountID)
	if err != nil {
		return accountapi.Notification{}, fmt.Errorf("read notification preferences: %w", err)
	}
	var dto accountapi.Notification
	if domain.Enabled(stored, category, domain.ChannelInApp) {
		insertedID, err := s.repo.InsertNotification(ctx, n)
		if err != nil {
			return accountapi.Notification{}, fmt.Errorf("insert notification: %w", err)
		}
		n.ID = insertedID
		dto = toAPINotification(n)
		notifyRealtime(ctx, s, accountID, NotificationCreated, dto)
	}

	// After the row, so a failed insert the bus will redeliver has not already mailed.
	s.mailNotification(ctx, req, category, stored)
	return dto, nil
}

// mailNotification sends the email channel's copy of a notification, when the account asked
// for that channel and there is an address worth sending to.
//
// Best-effort, like every other send in this service: the feed row is already written, so a
// relay that is down must not fail the call — the caller is a bus subscriber, and a returned
// error there buys a redelivered fact and a duplicate feed row to retry a mail nobody lost.
func (s *Service) mailNotification(ctx context.Context, req accountapi.CreateNotificationRequest,
	category domain.Category, stored []domain.Preference) {
	if req.MailKind == "" || !domain.Enabled(stored, category, domain.ChannelEmail) {
		return
	}
	accountID := req.AccountID.Int64()
	acc, err := s.repo.Get(ctx, accountID)
	if err != nil {
		s.log.Error("read account for notification email", "account_id", accountID, "err", err)
		return
	}
	// Verified only. An address nobody confirmed is somebody's typo or somebody else's
	// inbox, and what goes out here is what somebody bought and what they paid for it —
	// plus a bounce rate that costs this domain its ability to send at all. The two mails
	// that must reach an unverified address are the verification and the reset, and neither
	// comes through here.
	if acc.Email == nil || !acc.EmailVerified {
		return
	}
	s.send(ctx, notify.Message{
		Kind:   notify.Kind(req.MailKind),
		Email:  *acc.Email,
		Locale: acc.Profile.Locale,
		Params: req.Payload,
	})
}

func toAPINotification(n domain.Notification) accountapi.Notification {
	return accountapi.Notification{
		Category:  string(n.Category),
		Title:     n.Title,
		Payload:   n.Payload,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
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

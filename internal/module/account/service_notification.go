package account

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
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
	if len(rows) == 0 {
		return accountapi.Cursor[accountapi.Notification]{Data: []accountapi.Notification{}}, nil
	}
	// One profile read for the whole page, not one per row: the language is the reader's, and
	// the reader is the same person for every notification on it.
	locale := s.readerLocale(ctx, req.ActorID.Int64())
	out := make([]accountapi.Notification, 0, len(rows))
	for _, n := range rows {
		out = append(out, s.toAPINotification(locale, n))
	}
	return accountapi.Cursor[accountapi.Notification]{Data: out, NextCursor: next}, nil
}

// readerLocale is the language the feed is written in for one account.
//
// Best-effort: a profile that cannot be read falls back to the platform's default rather than
// failing the page, because a feed is worth more in the wrong language than not at all. The
// copybook falls back again on an unknown locale, so a truly broken read still renders.
func (s *Service) readerLocale(ctx context.Context, accountID int64) string {
	profile, err := s.repo.FindProfile(ctx, accountID)
	if err != nil {
		s.log.Error("read profile for notification locale", "account_id", accountID, "err", err)
		return defaultLocale
	}
	return profile.Locale
}

func (s *Service) GetUnreadCount(ctx context.Context, req accountapi.GetUnreadCountRequest) (accountapi.UnreadCount, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.UnreadCount{}, err
	}
	byCategory, err := s.repo.CountUnreadNotifications(ctx, req.ActorID.Int64())
	if err != nil {
		return accountapi.UnreadCount{}, fmt.Errorf("count unread notifications: %w", err)
	}
	return toAPIUnreadCount(byCategory), nil
}

// MarkNotificationsRead answers with the badge that follows, so the client does not have
// to ask for it in a second call it would make every single time.
//
// A list of ids clears exactly those rows; a bound clears everything up to an instant; neither
// clears the whole feed. The two are refused together rather than applied in some order,
// because "these three, and also everything from last Tuesday" is not a thing a reader asked
// for — it is a client sending a stale bound alongside a fresh click.
func (s *Service) MarkNotificationsRead(ctx context.Context, req accountapi.MarkNotificationsReadRequest) (accountapi.UnreadCount, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.UnreadCount{}, err
	}
	if len(req.IDs) > 0 && req.Before != nil {
		return accountapi.UnreadCount{}, errx.NewValidationError("send either ids or before, not both")
	}
	accountID := req.ActorID.Int64()
	if len(req.IDs) > 0 {
		ids := make([]int64, 0, len(req.IDs))
		for _, notificationID := range req.IDs {
			ids = append(ids, notificationID.Int64())
		}
		if err := s.repo.MarkNotificationsReadByIDs(ctx, accountID, ids); err != nil {
			return accountapi.UnreadCount{}, fmt.Errorf("mark notifications read: %w", err)
		}
	} else if err := s.repo.MarkNotificationsRead(ctx, accountID, req.Before); err != nil {
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
// The caller sends a kind and the facts. Everything else follows from the kind: which category
// the row files under and therefore which preference decides it, which words it renders as,
// which page it opens, and which mail template carries it. A caller that had to name its own
// title and its own mail template could name two that disagreed, and did.
//
// The feed row is the one this module owns a table for; the mail goes out through the notify
// provider and nothing records that it went, because a row standing for "we tried to email you"
// would be a second, weaker definition of the feed. Push and SMS are still a workflow's problem.
//
// The two channels are decided independently. Turning the feed off is not a way to stop the
// mail, and it used to be: the in-app preference returned early, in front of everything.
func (s *Service) CreateNotification(ctx context.Context, req accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Notification{}, err
	}
	accountID := req.AccountID.Int64()

	// The domain refuses a kind it has no vocabulary for, and derives the category from the one
	// it accepts — so the preference lookup below can never read an unknown category as "off"
	// the way DefaultPreference treats any pair it does not recognise.
	n, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID: accountID,
		Kind:      domain.Kind(req.Kind),
		Payload:   req.Payload,
	})
	if err != nil {
		return accountapi.Notification{}, err
	}
	spec, _ := domain.SpecOf(n.Kind)

	stored, err := s.repo.ListPreferences(ctx, accountID)
	if err != nil {
		return accountapi.Notification{}, fmt.Errorf("read notification preferences: %w", err)
	}
	var dto accountapi.Notification
	if domain.Enabled(stored, n.Category, domain.ChannelInApp) {
		insertedID, err := s.repo.InsertNotification(ctx, n)
		if err != nil {
			return accountapi.Notification{}, fmt.Errorf("insert notification: %w", err)
		}
		n.ID = insertedID
		dto = s.toAPINotification(s.readerLocale(ctx, accountID), n)
		notifyRealtime(ctx, s, accountID, NotificationCreated, dto)
	}

	// After the row, so a failed insert the bus will redeliver has not already mailed.
	s.mailNotification(ctx, n, spec, stored)
	return dto, nil
}

// mailNotification sends the email channel's copy of a notification, when the fact has a
// template, the account asked for that channel and there is an address worth sending to.
//
// Best-effort, like every other send in this service: the feed row is already written, so a
// relay that is down must not fail the call — the caller is a bus subscriber, and a returned
// error there buys a redelivered fact and a duplicate feed row to retry a mail nobody lost.
func (s *Service) mailNotification(ctx context.Context, n domain.Notification,
	spec domain.KindSpec, stored []domain.Preference) {
	if spec.Mail == "" || !domain.Enabled(stored, n.Category, domain.ChannelEmail) {
		return
	}
	acc, err := s.repo.Get(ctx, n.AccountID)
	if err != nil {
		s.log.Error("read account for notification email", "account_id", n.AccountID, "err", err)
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
		Kind:   notify.Kind(spec.Mail),
		Email:  *acc.Email,
		Locale: acc.Profile.Locale,
		Params: n.Payload,
	})
}

// toAPINotification writes the row out as the reader sees it: the words in their language, and
// the link resolved from the same payload the words came from.
func (s *Service) toAPINotification(locale string, n domain.Notification) accountapi.Notification {
	title, body := s.copy.Render(locale, n.Kind, n.Payload)
	var href string
	if spec, ok := domain.SpecOf(n.Kind); ok && spec.Href != nil {
		href = spec.Href(n.Payload)
	}
	return accountapi.Notification{
		ID:        id.Of[id.Notification](n.ID),
		Kind:      string(n.Kind),
		Category:  string(n.Category),
		Title:     title,
		Body:      body,
		Href:      href,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// toAPIUnreadCount fills in every category, zeros included, and sums the badge from the same
// map — so the bell and the filters are one answer rather than two that can disagree.
func toAPIUnreadCount(byCategory map[domain.Category]int64) accountapi.UnreadCount {
	out := accountapi.UnreadCount{ByCategory: make(map[string]int64, len(domain.Categories))}
	for _, category := range domain.Categories {
		n := byCategory[category]
		out.ByCategory[string(category)] = n
		out.Unread += n
	}
	return out
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

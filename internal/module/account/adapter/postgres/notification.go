package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common/dbx"
)

// ListNotifications reads one page of the feed, newest first.
//
// The cursor is a keyset bound on created_at rather than an offset, for two reasons:
// the table is a hypertable, so a time bound is what lets Postgres skip whole chunks,
// and rows arrive at the head constantly, which makes an offset drift.
//
// A notification scheduled for later is not in the feed yet — the row exists so the
// dispatch workflow has something to send, not so the user reads it early.
func (r *Repo) ListNotifications(ctx context.Context, q port.NotificationQuery) ([]domain.Notification, error) {
	const sql = `SELECT id, account_id, kind, category::text, payload, created_at, read_at, scheduled_at
	             FROM notification
	             WHERE account_id = @account_id
	               AND (scheduled_at IS NULL OR scheduled_at <= now())
	               AND (@category = '' OR category::text = @category)
	               AND (NOT @unread_only OR read_at IS NULL)
	               AND (@before::timestamptz IS NULL OR created_at < @before)
	             ORDER BY created_at DESC
	             LIMIT @limit`
	args := pgx.NamedArgs{
		"account_id":  q.AccountID,
		"category":    string(q.Category),
		"unread_only": q.UnreadOnly,
		"before":      dbx.NullTime(q.Before),
		"limit":       q.Limit,
	}
	rows, err := r.pool.Query(ctx, sql, args)
	if err != nil {
		return nil, fmt.Errorf("db query notifications: %w", err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.AccountID, &n.Kind, &n.Category, &n.Payload,
			&n.CreatedAt, &n.ReadAt, &n.ScheduledAt); err != nil {
			return nil, fmt.Errorf("db scan notification row: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate notifications: %w", err)
	}
	return out, nil
}

// InsertNotification writes one feed row and answers its generated id.
func (r *Repo) InsertNotification(ctx context.Context, n domain.Notification) (int64, error) {
	const q = `
		INSERT INTO notification (account_id, kind, category, payload, created_at, scheduled_at)
		VALUES (@account_id, @kind, @category, @payload, @created_at, @scheduled_at)
		RETURNING id`

	var id int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{
		"account_id":   n.AccountID,
		"kind":         string(n.Kind),
		"category":     string(n.Category),
		"payload":      dbx.JSONObject(n.Payload),
		"created_at":   n.CreatedAt,
		"scheduled_at": n.ScheduledAt,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("db insert notification: %w", err)
	}
	return id, nil
}

// CountUnreadNotifications answers the badge, which is read far more often than the
// feed itself. The partial index on unread rows is what keeps it cheap.
//
// Grouped by category rather than one total, because the sidebar shows a count per filter and
// the two used to be two queries whose answers could disagree. Only categories with unread
// rows come back; the total is their sum, which is what keeps it consistent with the breakdown
// by construction. Categories are absent, never zero — a caller reads a missing key as none.
func (r *Repo) CountUnreadNotifications(ctx context.Context, accountID int64) (map[domain.Category]int64, error) {
	const q = `SELECT category::text, COUNT(*) FROM notification
	           WHERE account_id = @account_id AND read_at IS NULL
	             AND (scheduled_at IS NULL OR scheduled_at <= now())
	           GROUP BY category`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db count unread notifications: %w", err)
	}
	defer rows.Close()

	out := make(map[domain.Category]int64, len(domain.Categories))
	for rows.Next() {
		var category domain.Category
		var n int64
		if err := rows.Scan(&category, &n); err != nil {
			return nil, fmt.Errorf("db scan unread notification count: %w", err)
		}
		out[category] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate unread notification counts: %w", err)
	}
	return out, nil
}

// MarkNotificationsRead clears everything up to an instant — the "read all" button, and the
// bound a reader who scrolled past fifty rows is marked against. A nil bound marks the whole
// feed. On a time-partitioned table a bound reads one range of chunks, which is why this is
// the bulk shape and MarkNotificationsReadByIDs is the per-row one.
func (r *Repo) MarkNotificationsRead(ctx context.Context, accountID int64, before *time.Time) error {
	const q = `UPDATE notification SET read_at = CURRENT_TIMESTAMP
	           WHERE account_id = @account_id AND read_at IS NULL
	             AND (@before::timestamptz IS NULL OR created_at <= @before)`
	if _, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"account_id": accountID, "before": before}); err != nil {
		return fmt.Errorf("db mark notifications read: %w", err)
	}
	return nil
}

// MarkNotificationsReadByIDs marks the rows the reader actually opened, and nothing else.
//
// It has no time bound, so it visits every chunk in the retention window — which is what
// CountUnreadNotifications already does on every page load, and for the same reason it is
// cheap: the partial index holds only an account's unread rows, and there are never many.
// The alternative was what this feed had before — opening one notification marked everything
// older read, because a bound was the only thing a row could be named by.
func (r *Repo) MarkNotificationsReadByIDs(ctx context.Context, accountID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	const q = `UPDATE notification SET read_at = CURRENT_TIMESTAMP
	           WHERE account_id = @account_id AND read_at IS NULL AND id = ANY(@ids)`
	args := pgx.NamedArgs{"account_id": accountID, "ids": dbx.Int64Array(ids)}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db mark notifications read by id: %w", err)
	}
	return nil
}

func (r *Repo) ListPreferences(ctx context.Context, accountID int64) ([]domain.Preference, error) {
	const q = `SELECT account_id, category::text, channel::text, is_enabled
	           FROM notification_preference WHERE account_id = @account_id`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query notification preferences: %w", err)
	}
	defer rows.Close()

	var out []domain.Preference
	for rows.Next() {
		var p domain.Preference
		if err := rows.Scan(&p.AccountID, &p.Category, &p.Channel, &p.IsEnabled); err != nil {
			return nil, fmt.Errorf("db scan notification preference row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate notification preferences: %w", err)
	}
	return out, nil
}

// SavePreferences applies one change set in a transaction: the deviations are stored,
// and a pair that went back to its default has its row deleted rather than storing the
// default again — that is what keeps the table sparse and the defaults free to change
// without a migration.
func (r *Repo) SavePreferences(ctx context.Context, accountID int64, store, remove []domain.Preference) error {
	if len(store) == 0 && len(remove) == 0 {
		return nil
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const upsert = `INSERT INTO notification_preference (account_id, category, channel, is_enabled)
		                VALUES (@account_id, @category, @channel, @is_enabled)
		                ON CONFLICT (account_id, category, channel) DO UPDATE
		                    SET is_enabled = EXCLUDED.is_enabled`
		for _, p := range store {
			args := pgx.NamedArgs{
				"account_id": accountID,
				"category":   string(p.Category),
				"channel":    string(p.Channel),
				"is_enabled": p.IsEnabled,
			}
			if _, err := tx.Exec(ctx, upsert, args); err != nil {
				if dbx.IsForeignKeyViolation(err) {
					return domain.ErrAccountNotFound
				}
				return fmt.Errorf("db upsert notification preference: %w", err)
			}
		}
		const del = `DELETE FROM notification_preference
		             WHERE account_id = @account_id AND category::text = @category AND channel::text = @channel`
		for _, p := range remove {
			args := pgx.NamedArgs{
				"account_id": accountID,
				"category":   string(p.Category),
				"channel":    string(p.Channel),
			}
			if _, err := tx.Exec(ctx, del, args); err != nil {
				return fmt.Errorf("db delete notification preference: %w", err)
			}
		}
		return nil
	})
}

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
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
	const sql = `SELECT id, account_id, category::text, title, payload, created_at, read_at, scheduled_at
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
		"before":      nullTime(q.Before),
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
		if err := rows.Scan(&n.ID, &n.AccountID, &n.Category, &n.Title, &n.Payload,
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

// CountUnreadNotifications answers the badge, which is read far more often than the
// feed itself. The partial index on unread rows is what keeps it cheap.
func (r *Repo) CountUnreadNotifications(ctx context.Context, accountID int64) (int64, error) {
	const q = `SELECT COUNT(*) FROM notification
	           WHERE account_id = @account_id AND read_at IS NULL
	             AND (scheduled_at IS NULL OR scheduled_at <= now())`
	var n int64
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&n); err != nil {
		return 0, fmt.Errorf("db count unread notifications: %w", err)
	}
	return n, nil
}

// MarkNotificationsRead takes a time bound, not a list of ids: on a time-partitioned
// table a set of ids has to be searched for in every chunk, while a bound reads one
// range. A nil bound marks the whole feed.
func (r *Repo) MarkNotificationsRead(ctx context.Context, accountID int64, before *time.Time) error {
	const q = `UPDATE notification SET read_at = CURRENT_TIMESTAMP
	           WHERE account_id = @account_id AND read_at IS NULL
	             AND (@before::timestamptz IS NULL OR created_at <= @before)`
	if _, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"account_id": accountID, "before": before}); err != nil {
		return fmt.Errorf("db mark notifications read: %w", err)
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
	return r.inTx(ctx, func(tx pgx.Tx) error {
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
				if isForeignKeyViolation(err) {
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

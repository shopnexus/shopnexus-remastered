package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
)

const deviceColumns = `id, account_id, platform::text, push_token, last_seen_at, created_at`

// scanDevice reads deviceColumns, in that order. pgx.Rows satisfies pgx.Row, so it serves
// the single-row read and the loop alike and the column list exists once.
func scanDevice(row pgx.Row) (domain.Device, error) {
	var d domain.Device
	err := row.Scan(&d.ID, &d.AccountID, &d.Platform, &d.PushToken, &d.LastSeenAt, &d.CreatedAt)
	if isNoRows(err) {
		return domain.Device{}, domain.ErrDeviceNotFound
	}
	if err != nil {
		return domain.Device{}, fmt.Errorf("db scan device: %w", err)
	}
	return d, nil
}

// UpsertDevice conflicts on the push token alone. That is the point: the token
// identifies an install, and the platform hands the same one to whoever signs in on
// that phone next, so the row has to move accounts rather than be rejected as a
// duplicate — otherwise the previous owner keeps getting that phone's notifications.
func (r *Repo) UpsertDevice(ctx context.Context, d *domain.Device) error {
	const q = `INSERT INTO device (account_id, platform, push_token)
	           VALUES (@account_id, @platform, @push_token)
	           ON CONFLICT (push_token) DO UPDATE
	               SET account_id = EXCLUDED.account_id,
	                   platform = EXCLUDED.platform,
	                   last_seen_at = CURRENT_TIMESTAMP
	           RETURNING id, last_seen_at, created_at`
	args := pgx.NamedArgs{
		"account_id": d.AccountID,
		"platform":   string(d.Platform),
		"push_token": d.PushToken,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&d.ID, &d.LastSeenAt, &d.CreatedAt); err != nil {
		if isForeignKeyViolation(err) {
			return domain.ErrAccountNotFound
		}
		return fmt.Errorf("db upsert device: %w", err)
	}
	return nil
}

func (r *Repo) FindDevice(ctx context.Context, id int64) (domain.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM device WHERE id = @id`
	return scanDevice(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

func (r *Repo) ListDevices(ctx context.Context, accountID int64) ([]domain.Device, error) {
	q := `SELECT ` + deviceColumns + `
	      FROM device WHERE account_id = @account_id ORDER BY last_seen_at DESC`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query devices: %w", err)
	}
	defer rows.Close()

	var out []domain.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate devices: %w", err)
	}
	return out, nil
}

func (r *Repo) DeleteDevice(ctx context.Context, id int64) error {
	const q = `DELETE FROM device WHERE id = @id`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return fmt.Errorf("db delete device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDeviceNotFound
	}
	return nil
}

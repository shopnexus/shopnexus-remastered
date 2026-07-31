package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/common/dbx"
)

// InsertFollow is idempotent: following twice is the same state as following once, so a
// conflict is success rather than an error a client has to special-case.
func (r *Repo) InsertFollow(ctx context.Context, followerID, followeeID int64) error {
	const q = `INSERT INTO follow (follower_id, followee_id)
	           VALUES (@follower_id, @followee_id)
	           ON CONFLICT DO NOTHING`
	args := pgx.NamedArgs{"follower_id": followerID, "followee_id": followeeID}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		if dbx.IsForeignKeyViolation(err) {
			return domain.ErrAccountNotFound
		}
		return fmt.Errorf("db insert follow: %w", err)
	}
	return nil
}

// DeleteFollow is idempotent for the same reason, so unfollowing something that was
// never followed answers 204.
func (r *Repo) DeleteFollow(ctx context.Context, followerID, followeeID int64) error {
	const q = `DELETE FROM follow WHERE follower_id = @follower_id AND followee_id = @followee_id`
	args := pgx.NamedArgs{"follower_id": followerID, "followee_id": followeeID}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db delete follow: %w", err)
	}
	return nil
}

// ListFollowing and ListFollowers are the same query from the two ends of the edge.
// Both join account for the display half, because a list of ids is not something a client
// can render, and both order by the edge's created_at — the index is (owner, created_at DESC).
func (r *Repo) ListFollowing(ctx context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error) {
	const q = `SELECT ` + followProfileColumns + `, COUNT(*) OVER () AS total_count
	           FROM follow f
	           JOIN account p ON p.id = f.followee_id
	           WHERE f.follower_id = @account_id
	           ORDER BY f.created_at DESC
	           LIMIT @limit OFFSET @offset`
	return r.queryFollowPage(ctx, q, accountID, offset, limit)
}

func (r *Repo) ListFollowers(ctx context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error) {
	const q = `SELECT ` + followProfileColumns + `, COUNT(*) OVER () AS total_count
	           FROM follow f
	           JOIN account p ON p.id = f.follower_id
	           WHERE f.followee_id = @account_id
	           ORDER BY f.created_at DESC
	           LIMIT @limit OFFSET @offset`
	return r.queryFollowPage(ctx, q, accountID, offset, limit)
}

// followProfileColumns is only what a summary shows. The rest of the profile is not
// read, because a follower list of a hundred rows has no use for a hundred bios.
const followProfileColumns = `p.id, p.name, COALESCE(p.avatar_resource_id, 0)`

func (r *Repo) queryFollowPage(ctx context.Context, q string, accountID int64, offset, limit int) ([]domain.Profile, int64, error) {
	args := pgx.NamedArgs{"account_id": accountID, "limit": limit, "offset": offset}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query follow page: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Profile
		total int64
	)
	for rows.Next() {
		var p domain.Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.AvatarResourceID, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan follow page row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate follow page: %w", err)
	}
	return out, total, nil
}

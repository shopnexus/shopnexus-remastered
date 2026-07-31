package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
)

const conversationColumns = `id, account_a_id, account_b_id, last_message_at,
	       account_a_read_at, account_b_read_at, created_at`

func scanConversation(row pgx.Row) (domain.Conversation, error) {
	var c domain.Conversation
	err := row.Scan(&c.ID, &c.AccountAID, &c.AccountBID, &c.LastMessageAt,
		&c.AccountAReadAt, &c.AccountBReadAt, &c.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Conversation{}, domain.ErrConversationNotFound
	}
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("db scan conversation: %w", err)
	}
	return c, nil
}

// EnsureConversation is an upsert on the ordered pair. Two people who start writing at
// the same moment must not end up with two threads, and the unique constraint is what
// decides — ON CONFLICT turns the loser into a read instead of an error.
func (r *Repo) EnsureConversation(ctx context.Context, one, other int64) (domain.Conversation, error) {
	c, err := domain.NewConversation(one, other)
	if err != nil {
		return domain.Conversation{}, err
	}
	const q = `INSERT INTO conversation (account_a_id, account_b_id)
	           VALUES (@a, @b)
	           ON CONFLICT (account_a_id, account_b_id) DO NOTHING`
	args := pgx.NamedArgs{"a": c.AccountAID, "b": c.AccountBID}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return domain.Conversation{}, fmt.Errorf("db open conversation: %w", err)
	}
	const read = `SELECT ` + conversationColumns + ` FROM conversation
	              WHERE account_a_id = @a AND account_b_id = @b`
	return scanConversation(r.pool.QueryRow(ctx, read, args))
}

func (r *Repo) FindConversation(ctx context.Context, id int64) (domain.Conversation, error) {
	const q = `SELECT ` + conversationColumns + ` FROM conversation WHERE id = @id`
	return scanConversation(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListConversations is the inbox. A participant sits on either side of the stored pair,
// so this is a UNION ALL of two ordered index scans merged by the outer ORDER BY —
// conversation_account_a_id_idx and its mirror — rather than one scan that sorts.
func (r *Repo) ListConversations(ctx context.Context, f port.InboxFilter) ([]domain.Conversation, error) {
	// Each branch is parenthesised: a branch of a UNION cannot carry its own ORDER BY and
	// LIMIT otherwise, and those are what keep each side an ordered index scan.
	const q = `SELECT ` + conversationColumns + ` FROM (
	             (SELECT ` + conversationColumns + ` FROM conversation
	              WHERE account_a_id = @account_id
	                AND (@before::timestamptz IS NULL OR last_message_at < @before::timestamptz)
	              ORDER BY last_message_at DESC
	              LIMIT @limit)
	             UNION ALL
	             (SELECT ` + conversationColumns + ` FROM conversation
	              WHERE account_b_id = @account_id
	                AND (@before::timestamptz IS NULL OR last_message_at < @before::timestamptz)
	              ORDER BY last_message_at DESC
	              LIMIT @limit)
	           ) threads
	           ORDER BY last_message_at DESC, id DESC
	           LIMIT @limit`
	args := pgx.NamedArgs{
		"account_id": f.AccountID,
		"before":     dbx.NullTime(f.Before),
		"limit":      f.Limit,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query conversations: %w", err)
	}
	defer rows.Close()

	var out []domain.Conversation
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate conversations: %w", err)
	}
	return out, nil
}

// SaveConversation writes the read marks. Guarded so a mark never moves backwards even
// if two requests race: the later timestamp wins in SQL as well as in the domain.
func (r *Repo) SaveConversation(ctx context.Context, c domain.Conversation) error {
	const q = `UPDATE conversation
	           SET account_a_read_at = GREATEST(account_a_read_at, @a_read_at),
	               account_b_read_at = GREATEST(account_b_read_at, @b_read_at)
	           WHERE id = @id`
	args := pgx.NamedArgs{
		"id": c.ID, "a_read_at": c.AccountAReadAt, "b_read_at": c.AccountBReadAt,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConversationNotFound
	}
	return nil
}

// UnreadCounts is one query for the whole page: everything after the caller's own mark
// that they did not send, per thread. Bounded by the mark, so chunk exclusion applies and
// an ancient unread message costs no more than a recent one.
//
// `IS DISTINCT FROM` rather than `<>` on purpose: a system message has a NULL sender, so
// it is nobody's own and counts as unread for both sides — an offer card is news to the
// person who countered as much as to the one who has not looked yet.
func (r *Repo) UnreadCounts(ctx context.Context, accountID int64, conversationIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	const q = `SELECT c.id, count(m.id)
	           FROM conversation c
	           LEFT JOIN message m ON m.conversation_id = c.id
	             AND m.sender_id IS DISTINCT FROM @account_id
	             AND m.deleted_at IS NULL
	             AND m.created_at > COALESCE(
	                   CASE WHEN c.account_a_id = @account_id THEN c.account_a_read_at
	                        ELSE c.account_b_read_at END,
	                   '-infinity'::timestamptz)
	           WHERE c.id = ANY(@ids)
	           GROUP BY c.id`
	args := pgx.NamedArgs{"account_id": accountID, "ids": conversationIDs}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query unread counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			conversationID int64
			unread         int64
		)
		if err := rows.Scan(&conversationID, &unread); err != nil {
			return nil, fmt.Errorf("db scan unread count: %w", err)
		}
		out[conversationID] = unread
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate unread counts: %w", err)
	}
	return out, nil
}

// UnreadTotal is the badge: everything unread across every thread, and how many threads
// have anything at all.
func (r *Repo) UnreadTotal(ctx context.Context, accountID int64) (int64, int64, error) {
	const q = `SELECT COALESCE(sum(unread), 0), count(*) FILTER (WHERE unread > 0)
	           FROM (
	             SELECT count(m.id) AS unread
	             FROM conversation c
	             LEFT JOIN message m ON m.conversation_id = c.id
	               AND m.sender_id IS DISTINCT FROM @account_id
	               AND m.deleted_at IS NULL
	               AND m.created_at > COALESCE(
	                     CASE WHEN c.account_a_id = @account_id THEN c.account_a_read_at
	                          ELSE c.account_b_read_at END,
	                     '-infinity'::timestamptz)
	             WHERE c.account_a_id = @account_id OR c.account_b_id = @account_id
	             GROUP BY c.id
	           ) counts`
	var total, threads int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&total, &threads)
	if err != nil {
		return 0, 0, fmt.Errorf("db query unread total: %w", err)
	}
	return total, threads, nil
}

// FindResources reads this module's own uploaded attachments — a photo sent in a thread
// belongs to chat, and its id only resolves here.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]common.Resource, error) {
	return dbx.NewResources(r.pool).Find(ctx, ids)
}

// Package postgres implements the chat port.Repository using pgx named args.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) Save(ctx context.Context, m *domain.Message) error {
	const q = `INSERT INTO messages (conversation_id, sender_id, body)
	           VALUES (@conversation_id, @sender_id, @body)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{"conversation_id": m.ConversationID, "sender_id": m.SenderID, "body": m.Body}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&m.ID, &m.CreatedAt); err != nil {
		return fmt.Errorf("db insert message: %w", err)
	}
	return nil
}

func (r *Repo) ListByConversation(ctx context.Context, conversationID int64, limit, offset int) ([]domain.Message, error) {
	const q = `SELECT id, conversation_id, sender_id, body, created_at
	           FROM messages WHERE conversation_id = @conversation_id
	           ORDER BY created_at DESC
	           LIMIT @limit OFFSET @offset`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"conversation_id": conversationID, "limit": limit, "offset": offset})
	if err != nil {
		return nil, fmt.Errorf("db query messages: %w", err)
	}
	defer rows.Close()

	var out []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan message row: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate messages: %w", err)
	}
	return out, nil
}

package postgres

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/module/common/dbx"
)

// A system message has no sender, which the column holds as NULL — so the scan reads it
// into a pointer and the entity keeps zero for "the backend said this".
const messageColumns = `id, conversation_id, sender_id, type::text, body, attachments,
	       metadata, created_at, edited_at, deleted_at, reply_to_id, reply_to_created_at`

func scanMessage(row pgx.Row) (domain.Message, error) {
	var (
		m        domain.Message
		senderID *int64
		metadata []byte
		replyID  *int64
		replyAt  *time.Time
	)
	err := row.Scan(&m.ID, &m.ConversationID, &senderID, &m.Type, &m.Body, &m.Attachments,
		&metadata, &m.CreatedAt, &m.EditedAt, &m.DeletedAt, &replyID, &replyAt)
	if dbx.IsNoRows(err) {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("db scan message: %w", err)
	}
	if senderID != nil {
		m.SenderID = *senderID
	}
	// The CHECK keeps these two together, so one test covers both.
	if replyID != nil && replyAt != nil {
		m.ReplyTo = &domain.MessageRef{ID: *replyID, CreatedAt: *replyAt}
	}
	// One JSONB column carries both halves: what the sender pointed at, and what the
	// backend rendered. They are read apart because only one of them is the client's.
	if len(metadata) > 0 {
		var envelope messageMetadata
		if err := json.Unmarshal(metadata, &envelope); err != nil {
			return domain.Message{}, fmt.Errorf("decode message metadata: %w", err)
		}
		m.Refs, m.Card = envelope.Refs, envelope.Card
	}
	return m, nil
}

// messageMetadata is the shape of the metadata column. Declared once, so a reader and a
// writer cannot disagree about which half is which.
type messageMetadata struct {
	Refs map[string]any `json:"refs,omitempty"`
	Card map[string]any `json:"card,omitempty"`
}

// InsertMessage appends to the thread and moves its last_message_at in one transaction.
// An inbox ordered by a timestamp the message did not set would sort wrongly for as long
// as the two disagreed, which under a retry is forever.
func (r *Repo) InsertMessage(ctx context.Context, m *domain.Message) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO message
		             (conversation_id, sender_id, type, body, attachments, metadata,
		              reply_to_id, reply_to_created_at)
		           VALUES (@conversation_id, @sender_id, @type, @body, @attachments, @metadata,
		                   @reply_to_id, @reply_to_created_at)
		           RETURNING id, created_at`
		metadata, err := json.Marshal(messageMetadata{Refs: m.Refs, Card: m.Card})
		if err != nil {
			return fmt.Errorf("encode message metadata: %w", err)
		}
		args := pgx.NamedArgs{
			"conversation_id":     m.ConversationID,
			"sender_id":           dbx.NullID(m.SenderID),
			"type":                m.Type,
			"body":                m.Body,
			"attachments":         dbx.Int64Array(m.Attachments),
			"metadata":            metadata,
			"reply_to_id":         nil,
			"reply_to_created_at": nil,
		}
		if m.ReplyTo != nil {
			args["reply_to_id"] = m.ReplyTo.ID
			args["reply_to_created_at"] = m.ReplyTo.CreatedAt
		}
		if err := tx.QueryRow(ctx, q, args).Scan(&m.ID, &m.CreatedAt); err != nil {
			return fmt.Errorf("db insert message: %w", err)
		}
		const touch = `UPDATE conversation SET last_message_at = @at WHERE id = @id`
		touchArgs := pgx.NamedArgs{"id": m.ConversationID, "at": m.CreatedAt}
		if _, err := tx.Exec(ctx, touch, touchArgs); err != nil {
			return fmt.Errorf("db touch conversation: %w", err)
		}
		return nil
	})
}

// FindMessage reads one by id. The primary key includes created_at because the table is a
// hypertable, so this is a scan across chunks rather than a point lookup — which is why
// nothing on the hot path uses it.
func (r *Repo) FindMessage(ctx context.Context, id int64) (domain.Message, error) {
	const q = `SELECT ` + messageColumns + ` FROM message WHERE id = @id`
	return scanMessage(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// FindMessageAt is the point lookup FindMessage's own comment describes: id and
// created_at together are the hypertable's primary key, so this hits one chunk instead of
// scanning all of them. The route behind an edit or a redaction always has created_at, so
// this is what they use.
func (r *Repo) FindMessageAt(ctx context.Context, id int64, createdAt time.Time) (domain.Message, error) {
	const q = `SELECT ` + messageColumns + ` FROM message WHERE id = @id AND created_at = @created_at`
	return scanMessage(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id, "created_at": createdAt}))
}

// SaveMessage writes an edit or a redaction. Keyed by id and created_at, which the caller
// read: a hypertable's primary key includes its partitioning column.
func (r *Repo) SaveMessage(ctx context.Context, m domain.Message) error {
	const q = `UPDATE message
	           SET body = @body, attachments = @attachments, metadata = @metadata,
	               edited_at = @edited_at, deleted_at = @deleted_at
	           WHERE id = @id AND created_at = @created_at`
	metadata, err := json.Marshal(messageMetadata{Refs: m.Refs, Card: m.Card})
	if err != nil {
		return fmt.Errorf("encode message metadata: %w", err)
	}
	args := pgx.NamedArgs{
		"id": m.ID, "created_at": m.CreatedAt, "body": m.Body,
		"attachments": dbx.Int64Array(m.Attachments), "metadata": metadata,
		"edited_at": m.EditedAt, "deleted_at": m.DeletedAt,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMessageNotFound
	}
	return nil
}

// ListMessages pages a thread newest first on a (created_at, id) cursor rather than an
// offset, so chunk exclusion can skip chunks instead of scanning all of them — and so a
// message arriving mid-read cannot shift the page. The tuple comparison is what keeps two
// messages sharing created_at exactly (the same transaction wrote them) from having one
// silently skipped at the page boundary.
func (r *Repo) ListMessages(ctx context.Context, f port.HistoryFilter) ([]domain.Message, error) {
	const q = `SELECT ` + messageColumns + ` FROM message
	           WHERE conversation_id = @conversation_id
	             AND (@before::timestamptz IS NULL OR (created_at, id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	args := pgx.NamedArgs{
		"conversation_id": f.ConversationID,
		"before":          dbx.NullTime(f.Before),
		"before_id":       f.BeforeID,
		"limit":           f.Limit,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query messages: %w", err)
	}
	defer rows.Close()

	var out []domain.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate messages: %w", err)
	}
	return out, nil
}

// LastMessages is what an inbox row shows: one message per thread, chosen by a lateral
// join so a page of twenty threads is one query rather than twenty histories.
func (r *Repo) LastMessages(ctx context.Context, conversationIDs []int64) (map[int64]domain.Message, error) {
	out := make(map[int64]domain.Message, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	const q = `SELECT m.id, m.conversation_id, m.sender_id, m.type::text, m.body,
	                  m.attachments, m.metadata, m.created_at, m.edited_at, m.deleted_at,
	                  m.reply_to_id, m.reply_to_created_at
	           FROM unnest(@ids::bigint[]) AS t(conversation_id)
	           JOIN LATERAL (
	             SELECT * FROM message
	             WHERE conversation_id = t.conversation_id
	             ORDER BY created_at DESC
	             LIMIT 1
	           ) m ON true`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": conversationIDs})
	if err != nil {
		return nil, fmt.Errorf("db query last messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out[m.ConversationID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate last messages: %w", err)
	}
	return out, nil
}

// QuotedMessages resolves the messages a page of replies points at — one query for the
// page, not one per reply.
//
// Both halves of each reference are used in the join, not just the id: `created_at` is the
// hypertable's partitioning column, so the equality is what lets chunk exclusion go
// straight to the chunk holding the quoted message instead of scanning all of them.
//
// Keyed by id alone on the way out. `id` is `GENERATED ALWAYS AS IDENTITY` and so unique
// across the table on its own; the instant is in the primary key because TimescaleDB
// requires the partitioning column there, not because the id needs help.
func (r *Repo) QuotedMessages(ctx context.Context, refs []domain.MessageRef) (map[int64]domain.Message, error) {
	out := make(map[int64]domain.Message, len(refs))
	if len(refs) == 0 {
		return out, nil
	}

	ids := make([]int64, 0, len(refs))
	ats := make([]time.Time, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
		ats = append(ats, ref.CreatedAt)
	}

	const q = `SELECT m.id, m.conversation_id, m.sender_id, m.type::text, m.body,
	                  m.attachments, m.metadata, m.created_at, m.edited_at, m.deleted_at,
	                  m.reply_to_id, m.reply_to_created_at
	           FROM unnest(@ids::bigint[], @ats::timestamptz[]) AS t(id, created_at)
	           JOIN message m ON m.id = t.id AND m.created_at = t.created_at`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": ids, "ats": ats})
	if err != nil {
		return nil, fmt.Errorf("db query quoted messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate quoted messages: %w", err)
	}
	return out, nil
}

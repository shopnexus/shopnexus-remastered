// Package port: interface the chat adapter must satisfy.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/common"
)

// InboxFilter pages a participant's threads, latest activity first. The cursor is a
// timestamp rather than an offset: an inbox moves under the reader, and an offset would
// skip or repeat a thread whenever it does.
type InboxFilter struct {
	AccountID int64
	Before    time.Time
	Limit     int
}

// HistoryFilter pages one thread, newest first, on a created_at cursor — which is what
// lets chunk exclusion skip whole chunks of the hypertable instead of scanning them.
type HistoryFilter struct {
	ConversationID int64
	Before         time.Time
	Limit          int
}

type Repository interface {
	// EnsureConversation returns the pair's thread, opening it if this is the first
	// message. One thread per pair, so this is an upsert rather than a create: two
	// people who start writing at the same moment must not end up with two threads.
	EnsureConversation(ctx context.Context, one, other int64) (domain.Conversation, error)
	FindConversation(ctx context.Context, id int64) (domain.Conversation, error)
	ListConversations(ctx context.Context, f InboxFilter) ([]domain.Conversation, error)
	SaveConversation(ctx context.Context, c domain.Conversation) error

	// InsertMessage appends to the thread and moves its last_message_at in the same
	// transaction: an inbox ordered by a timestamp the message did not set would sort
	// wrongly for as long as the two disagreed.
	InsertMessage(ctx context.Context, m *domain.Message) error
	FindMessage(ctx context.Context, id int64) (domain.Message, error)
	SaveMessage(ctx context.Context, m domain.Message) error
	ListMessages(ctx context.Context, f HistoryFilter) ([]domain.Message, error)
	// LastMessage is what an inbox row shows. Separate from the page, because a page of
	// threads needs one per thread rather than a history each.
	LastMessages(ctx context.Context, conversationIDs []int64) (map[int64]domain.Message, error)

	// UnreadCounts answers, per thread, how many of the counterparty's messages fall
	// after the caller's read mark — one query for the whole page.
	UnreadCounts(ctx context.Context, accountID int64, conversationIDs []int64) (map[int64]int64, error)
	// UnreadTotal is the badge: everything unread across every thread.
	UnreadTotal(ctx context.Context, accountID int64) (total int64, threads int64, err error)

	// FindResources reads this module's own uploaded attachments.
	FindResources(ctx context.Context, ids []int64) ([]common.Resource, error)
}

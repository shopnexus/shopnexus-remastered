// Package chatapi is the published contract of the chat service: the thread a pair of
// accounts shares, its messages, and the read marks that make an unread badge one row
// rather than a status per message.
//
// Order calls PostSystemMessage when a negotiation moves: the card it posts carries the
// offer's id and nothing else, because the terms live on the offer.
package chatapi

import (
	"context"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// Conversation is one inbox row: who it is with, what was said last, and how much of it
// the caller has not read.
type Conversation struct {
	ID           id.ID[id.Conversation]    `json:"id"`
	Counterparty accountapi.AccountSummary `json:"counterparty"`
	LastMessage  *Message                  `json:"last_message"`
	// LastMessageAt starts at the creation time, so an empty thread still sorts
	// predictably in the inbox.
	LastMessageAt time.Time `json:"last_message_at"`
	Unread        int64     `json:"unread"`
	// ReadAt is the caller's own mark; CounterpartyReadAt is the other side's, which is
	// what a read receipt is read from.
	ReadAt             *time.Time `json:"read_at"`
	CounterpartyReadAt *time.Time `json:"counterparty_read_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

type ConversationPage struct {
	Data []Conversation `json:"data"`
	Meta CursorInfo     `json:"meta"`
}

type Message struct {
	ID             id.ID[id.Message]      `json:"id"`
	ConversationID id.ID[id.Conversation] `json:"conversation_id"`
	// SenderID is null on a system message: that one is the backend's word.
	SenderID *id.ID[id.Account]   `json:"sender_id"`
	Type     string               `json:"type"`
	Body     string               `json:"body"`
	Images   []common.ResourceDTO `json:"attachments"`
	// Refs is what the sender pointed at — a listing, a variant, an order.
	Refs map[string]any `json:"refs,omitempty"`
	// Card is what a system message renders. For a price negotiation it is the offer's
	// id and nothing else, so a counter-offer cannot leave an old price on screen.
	Card      map[string]any `json:"card,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	EditedAt  *time.Time     `json:"edited_at,omitempty"`
	DeletedAt *time.Time     `json:"deleted_at,omitempty"`
}

type MessagePage struct {
	Data []Message  `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// CursorInfo is the cursor meta a chat list answers with. A timestamp cursor, not an
// offset: an inbox moves under the reader and an offset would skip or repeat a row.
type CursorInfo struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type UnreadCount struct {
	Unread int64 `json:"unread"`
	// Conversations is how many threads have anything unread — the badge next to the
	// inbox, as opposed to the one on the app icon.
	Conversations int64 `json:"conversations"`
}

// --- requests ---

type ListConversationsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

// StartConversationRequest opens the thread with one account, or answers the one that
// already exists: there is one per pair, so this is idempotent by construction.
type StartConversationRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"account_id" validate:"required"`
}

type GetConversationRequest struct {
	ActorID id.ID[id.Account]      `json:"-" validate:"required"`
	ID      id.ID[id.Conversation] `json:"-" validate:"required"`
}

type ListMessagesRequest struct {
	ActorID id.ID[id.Account]      `json:"-" validate:"required"`
	ID      id.ID[id.Conversation] `json:"-" validate:"required"`
	Cursor  string                 `json:"-"`
	Limit   int                    `json:"-" validate:"required,min=1,max=100"`
}

type SendMessageRequest struct {
	ActorID        id.ID[id.Account]      `json:"-" validate:"required"`
	ConversationID id.ID[id.Conversation] `json:"-" validate:"required"`
	Body           string                 `json:"body" validate:"max=4000"`
	Attachments    []id.ID[id.Resource]   `json:"attachments,omitempty" validate:"max=10"`
	Refs           map[string]any         `json:"refs,omitempty"`
}

type MarkConversationReadRequest struct {
	ActorID id.ID[id.Account]      `json:"-" validate:"required"`
	ID      id.ID[id.Conversation] `json:"-" validate:"required"`
	// Before is how far the caller has read. Absent means "everything so far", which is
	// what opening a thread does; a value is what a client that tracks its own scroll
	// sends. Named to match the spec's field, since the wire body is what a client codes
	// against.
	Before *time.Time `json:"before,omitempty"`
}

type UpdateMessageRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Message] `json:"-" validate:"required"`
	Body    string            `json:"body" validate:"required,max=4000"`
	// CreatedAt is the message's own, off the query string: message is a hypertable whose
	// primary key is (id, created_at), so this is what turns the lookup into a point
	// lookup instead of a scan of every chunk.
	CreatedAt time.Time `json:"-" validate:"required"`
}

type RedactMessageRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Message] `json:"-" validate:"required"`
	// CreatedAt is the message's own, off the query string — see UpdateMessageRequest.
	CreatedAt time.Time `json:"-" validate:"required"`
}

// GetMessageRequest reads one message. A participant may read their own thread's; a
// moderator may read any, because a harassment report is judged on the message itself.
type GetMessageRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Message] `json:"-" validate:"required"`
}

type UnreadCountRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// PostSystemMessageRequest is what another module posts into a pair's thread — an offer
// card, an order update. No route: a client forging one would be asserting something the
// platform is supposed to attest.
type PostSystemMessageRequest struct {
	// The two sides. The thread is opened if they have never spoken.
	AccountAID id.ID[id.Account] `validate:"required"`
	AccountBID id.ID[id.Account] `validate:"required"`
	Body       string            `validate:"max=4000"`
	// Card is the payload the client renders — for a negotiation, the offer's id.
	Card map[string]any
}

type Service interface {
	ListConversations(ctx context.Context, req ListConversationsRequest) (ConversationPage, error)
	StartConversation(ctx context.Context, req StartConversationRequest) (Conversation, error)
	GetConversation(ctx context.Context, req GetConversationRequest) (Conversation, error)
	GetUnreadCount(ctx context.Context, req UnreadCountRequest) (UnreadCount, error)

	ListMessages(ctx context.Context, req ListMessagesRequest) (MessagePage, error)
	SendMessage(ctx context.Context, req SendMessageRequest) (Message, error)
	MarkConversationRead(ctx context.Context, req MarkConversationReadRequest) (Conversation, error)
	UpdateMessage(ctx context.Context, req UpdateMessageRequest) (Message, error)
	RedactMessage(ctx context.Context, req RedactMessageRequest) error

	// --- called by another module, not by a route ---

	// PostSystemMessage puts a card into the pair's thread, opening it if they have never
	// spoken. Order calls it when a negotiation moves.
	PostSystemMessage(ctx context.Context, req PostSystemMessageRequest) (Message, error)

	// GetMessage reads one message: a participant's own, or any of them for a moderator.
	// Trust calls it to check a reported message exists and to show it in the queue.
	GetMessage(ctx context.Context, req GetMessageRequest) (Message, error)
}

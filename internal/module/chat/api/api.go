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
	ID id.ID[id.Conversation] `json:"id"`
	// TicketID is set on a support thread, and null on an ordinary one. It is what lets a client
	// render the ticket's own header above the same message list.
	TicketID *id.ID[id.Ticket] `json:"ticket_id"`
	// Counterparty is the other side. On a ticket thread that is the support desk itself, never the
	// moderator on shift: a verdict is the platform's, not a named person's to be argued with.
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
	// SenderID is null on a system message: that one is the backend's word. It is also null on a
	// support reply seen by the requester — see FromSupport.
	SenderID *id.ID[id.Account] `json:"sender_id"`
	// FromSupport marks a reply the desk wrote. The requester is told that much and no more; staff
	// reading their own queue see the real sender, because a colleague's name is what makes a
	// thread reviewable.
	FromSupport bool                 `json:"from_support"`
	Type        string               `json:"type"`
	Body        string               `json:"body"`
	Images      []common.ResourceDTO `json:"attachments"`
	// Refs is what the sender pointed at — a listing, a variant, an order. Always present, and
	// `{}` when there is nothing: the spec marks it required, so omitting the empty case made
	// every message unreadable to a generated client whose field is non-nullable. Which is most
	// messages — hardly any carry a reference.
	Refs map[string]any `json:"refs"`
	// Card is what a system message renders. For a price negotiation it is the offer's
	// id and nothing else, so a counter-offer cannot leave an old price on screen.
	Card      map[string]any `json:"card"`
	CreatedAt time.Time      `json:"created_at"`
	EditedAt  *time.Time     `json:"edited_at"`
	DeletedAt *time.Time     `json:"deleted_at"`
}

type MessagePage struct {
	Data []Message  `json:"data"`
	Meta CursorInfo `json:"meta"`
}

// CursorInfo is the cursor meta a chat list answers with. A timestamp cursor, not an
// offset: an inbox moves under the reader and an offset would skip or repeat a row.
type CursorInfo struct {
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type UnreadCount struct {
	Unread int64 `json:"unread"`
	// Conversations is how many threads have anything unread — the badge next to the
	// inbox, as opposed to the one on the app icon.
	Conversations int64 `json:"conversations"`
}

// DeletedMessageRef is enough to find and drop a message from a rendered thread. Not the
// whole Message: a deleted row's body is gone, and sending an emptied entity would read as
// an edit.
type DeletedMessageRef struct {
	ID             id.ID[id.Message]      `json:"id"`
	ConversationID id.ID[id.Conversation] `json:"conversation_id"`
	// CreatedAt is the message's own instant — the hypertable needs it to locate the row.
	CreatedAt time.Time `json:"created_at"`
}

// ConversationReadMark is how far one participant has read a thread.
type ConversationReadMark struct {
	ConversationID id.ID[id.Conversation] `json:"conversation_id"`
	// ReaderID is who read it — always the other participant, never the recipient.
	ReaderID id.ID[id.Account] `json:"reader_id"`
	ReadAt   time.Time         `json:"read_at"`
}

// --- requests ---

type ListConversationsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Cursor  string            `json:"-"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

// StartConversationRequest opens the thread with one account, or answers the one that
// already exists: there is one per pair, so this is idempotent by construction.
// OpenTicketThreadRequest is trust's call. Body and Attachments are what the requester submitted:
// they become the thread's first message, which is why the ticket keeps neither.
type OpenTicketThreadRequest struct {
	RequesterID id.ID[id.Account]    `json:"-" validate:"required"`
	TicketID    id.ID[id.Ticket]     `json:"-" validate:"required"`
	Body        string               `json:"-" validate:"max=4000"`
	Attachments []id.ID[id.Resource] `json:"-" validate:"max=10"`
}

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
	Attachments    []id.ID[id.Resource]   `json:"attachments" validate:"max=10"`
	Refs           map[string]any         `json:"refs"`
}

type MarkConversationReadRequest struct {
	ActorID id.ID[id.Account]      `json:"-" validate:"required"`
	ID      id.ID[id.Conversation] `json:"-" validate:"required"`
	// Before is how far the caller has read. Absent means "everything so far", which is
	// what opening a thread does; a value is what a client that tracks its own scroll
	// sends. Named to match the spec's field, since the wire body is what a client codes
	// against.
	Before *time.Time `json:"before"`
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

// PostTicketMessageRequest is a fact posted into a ticket's thread by the module that decided it —
// a refund verdict. The requester travels with it because this opens the thread if it is not there
// yet: a decision has to reach the person who asked for it even when the earlier open failed.
type PostTicketMessageRequest struct {
	RequesterID id.ID[id.Account] `validate:"required"`
	TicketID    id.ID[id.Ticket]  `validate:"required"`
	Body        string            `validate:"max=4000"`
	// Card is what the client renders — for a refund verdict, the refund's id.
	Card map[string]any
}

type Service interface {
	// CreateUpload reserves a row and a presigned slot for a message attachment;
	// ConfirmUpload makes it real once the bytes are at the store. Until then the resource
	// resolves to nothing, so a half-finished upload cannot be attached to a message.
	CreateUpload(ctx context.Context, req common.CreateUploadRequest) (common.UploadSlotDTO, error)
	ConfirmUpload(ctx context.Context, req common.ConfirmUploadRequest) (common.ResourceDTO, error)

	ListConversations(ctx context.Context, req ListConversationsRequest) (ConversationPage, error)
	StartConversation(ctx context.Context, req StartConversationRequest) (Conversation, error)
	// OpenTicketThread is the conversation behind a ticket, with the requester's own words as its
	// first message and their photos as its attachments — so a ticket needs no body column and no
	// second upload path. Idempotent on TicketID: the ticket row is written first in another
	// schema, and this is the half that may have to be retried or repaired later.
	OpenTicketThread(ctx context.Context, req OpenTicketThreadRequest) (Conversation, error)
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

	// PostTicketMessage puts a card into a ticket's thread. Trust calls it when a verdict was
	// decided elsewhere — the requester reads the outcome where they raised it.
	PostTicketMessage(ctx context.Context, req PostTicketMessageRequest) (Message, error)

	// GetMessage reads one message: a participant's own, or any of them for a moderator.
	// Trust calls it to check a reported message exists and to show it in the queue.
	GetMessage(ctx context.Context, req GetMessageRequest) (Message, error)
}

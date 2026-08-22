// Package domain: chat entity + pure business rules.
package domain

import (
	"strings"
	"time"

	"shopnexus/internal/shared/validation"
)

// Message types (kebab-case, mirrors the message_type enum).
const (
	TypeUser   = "user"
	TypeSystem = "system"
)

// MessageRef names one message. Both halves, because "message" is a hypertable whose
// primary key is (id, created_at): the instant is what makes finding it a point lookup in
// one chunk rather than a scan of every chunk, which is also why the edit and redact routes
// take it off the query string.
type MessageRef struct {
	ID        int64     `validate:"required"`
	CreatedAt time.Time `validate:"required"`
}

// Message is one entry in a thread. A system message has no sender: it is the backend's
// word — an offer card, an order update — which is why the schema ties the two together
// with a CHECK and why a client cannot write one.
type Message struct {
	ID             int64
	ConversationID int64 `validate:"required"`
	// SenderID is zero on a system message, which is the NULL the column holds.
	SenderID int64
	Type     string `validate:"required,oneof=user system"`
	Body     string `validate:"max=4000"`
	// Attachments are resource ids from this module's own resource table.
	Attachments []int64
	// Refs is what the sender pointed at — a listing, a variant, an order. Client-supplied,
	// so it can carry a reference but never assert anything about it.
	Refs map[string]any
	// Card is what a system message renders, and only the backend writes it: for a price
	// negotiation that is the offer's id and nothing else, so a counter-offer cannot leave
	// an old price on screen.
	Card      map[string]any
	CreatedAt time.Time
	EditedAt  *time.Time
	// DeletedAt is a redaction — the sender unsending, or moderation acting on a report.
	// The row stays so a thread has no unexplained gaps.
	DeletedAt *time.Time
	// ReplyTo is the message this one answers, and nil on an ordinary one. A reference, not
	// a copy: the quote is resolved when the thread is read, so an edit to the original
	// shows through and a redaction reads as redacted instead of leaving its old words in
	// every reply to it.
	ReplyTo *MessageRef
}

// NewMessage is a person speaking. A message with neither text nor an attachment says
// nothing, so it is refused rather than stored.
//
// replyTo is the message being answered, or nil. Whether that message is one the sender may
// answer — the same thread, and one that exists — is not knowable here: it is a lookup, so
// the service checks it before calling this.
func NewMessage(conversationID, senderID int64, body string, attachments []int64, refs map[string]any, replyTo *MessageRef) (Message, error) {
	m := Message{
		ConversationID: conversationID,
		SenderID:       senderID,
		Type:           TypeUser,
		Body:           strings.TrimSpace(body),
		Attachments:    attachments,
		Refs:           refs,
		ReplyTo:        replyTo,
	}
	if senderID == 0 {
		return Message{}, ErrNotAParticipant
	}
	if m.Body == "" && len(m.Attachments) == 0 {
		return Message{}, ErrEmptyMessage
	}
	if err := validation.Default().Struct(m); err != nil {
		return Message{}, validation.AsError(err)
	}
	return m, nil
}

// NewSystemMessage is the backend speaking: an offer card, an order update. No sender, and
// a card instead of a body, which is what a client is never allowed to produce.
func NewSystemMessage(conversationID int64, body string, card map[string]any) (Message, error) {
	m := Message{
		ConversationID: conversationID,
		Type:           TypeSystem,
		Body:           strings.TrimSpace(body),
		Card:           card,
	}
	if err := validation.Default().Struct(m); err != nil {
		return Message{}, validation.AsError(err)
	}
	return m, nil
}

// IsLive reports whether the message still has content. A redacted row is kept for the
// thread's continuity, not for what it said.
func (m Message) IsLive() bool { return m.DeletedAt == nil }

// Edit rewrites the body. Only the sender, only a user message, and never one already
// unsent — an edit of a redaction would bring the content back.
func (m *Message) Edit(senderID int64, body string) error {
	if err := m.mutableBy(senderID); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" && len(m.Attachments) == 0 {
		return ErrEmptyMessage
	}
	m.Body = trimmed
	m.EditedAt = new(time.Now())
	return nil
}

// Redact is unsending: the content goes, the row stays. A moderator acting on a report
// takes the same path, which is why the actor is a parameter rather than assumed.
func (m *Message) Redact(actorID int64, moderator bool) error {
	if !moderator {
		if err := m.mutableBy(actorID); err != nil {
			return err
		}
	} else if !m.IsLive() {
		return ErrMessageRedacted
	}
	m.Body = ""
	m.Attachments = nil
	m.Card = nil
	m.DeletedAt = new(time.Now())
	return nil
}

func (m Message) mutableBy(senderID int64) error {
	if m.Type == TypeSystem {
		return ErrSystemMessage
	}
	if m.SenderID != senderID {
		return ErrNotTheSender
	}
	if !m.IsLive() {
		return ErrMessageRedacted
	}
	return nil
}

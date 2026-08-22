package domain_test

import (
	"errors"
	"testing"
	"time"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/shared/errx"
)

func TestNewMessage_Valid(t *testing.T) {
	m, err := domain.NewMessage(3, 7, "Hello", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Body != "Hello" || m.Type != domain.TypeUser {
		t.Fatalf("message = %+v, want a user message saying Hello", m)
	}
}

// A message with neither text nor an attachment says nothing. An attachment alone does,
// which is what makes this a rule about content rather than about the body field.
func TestNewMessage_NeedsSomethingToSay(t *testing.T) {
	if _, err := domain.NewMessage(3, 7, "   ", nil, nil, nil); !errors.Is(err, domain.ErrEmptyMessage) {
		t.Fatalf("empty message = %v, want ErrEmptyMessage", err)
	}
	if _, err := domain.NewMessage(3, 7, "", []int64{42}, nil, nil); err != nil {
		t.Fatalf("a photo with no caption was refused: %v", err)
	}
}

// A system message is the backend's word — an offer card, an order update — so it has no
// sender and a client can neither write nor change one.
func TestSystemMessage_IsNotAUsers(t *testing.T) {
	m, err := domain.NewSystemMessage(3, "offer updated", map[string]any{"offer_id": 9})
	if err != nil {
		t.Fatalf("NewSystemMessage: %v", err)
	}
	if m.SenderID != 0 || m.Type != domain.TypeSystem {
		t.Fatalf("message = %+v, want a senderless system message", m)
	}
	if err := m.Edit(7, "forged"); !errors.Is(err, domain.ErrSystemMessage) {
		t.Fatalf("Edit = %v, want ErrSystemMessage", err)
	}
	if err := m.Redact(7, false); !errors.Is(err, domain.ErrSystemMessage) {
		t.Fatalf("Redact = %v, want ErrSystemMessage", err)
	}
}

// Only the sender edits, and never something already unsent — an edit of a redaction
// would bring the content back.
func TestMessage_EditAndRedact(t *testing.T) {
	m, err := domain.NewMessage(3, 7, "Hello", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := m.Edit(8, "not mine"); !errors.Is(err, domain.ErrNotTheSender) {
		t.Fatalf("Edit by a stranger = %v, want ErrNotTheSender", err)
	}
	if err := m.Edit(7, "Hello again"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if m.Body != "Hello again" || m.EditedAt == nil {
		t.Fatalf("message = %+v, want it edited and marked", m)
	}

	if err := m.Redact(7, false); err != nil {
		t.Fatalf("Redact: %v", err)
	}
	// The row survives so the thread has no unexplained gaps; the content does not.
	if m.Body != "" || m.IsLive() {
		t.Fatalf("message = %+v, want it emptied and marked deleted", m)
	}
	if err := m.Edit(7, "back again"); !errors.Is(err, domain.ErrMessageRedacted) {
		t.Fatalf("Edit after redaction = %v, want ErrMessageRedacted", err)
	}
	// A moderator acting on a report takes the same path, and a second redaction is a
	// conflict rather than a silent success.
	if err := m.Redact(99, true); !errors.Is(err, domain.ErrMessageRedacted) {
		t.Fatalf("moderator re-redaction = %v, want ErrMessageRedacted", err)
	}
}

func TestNewMessage_EmptyBody(t *testing.T) {
	_, err := domain.NewMessage(3, 7, "", nil, nil, nil)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 422 {
		t.Fatalf("expected an unprocessable entity, got %v", err)
	}
}

// The pair is stored ordered, so the same two accounts always produce the same row
// whichever of them starts the thread — and nobody gets a thread with themselves.
func TestNewConversation_OrdersThePair(t *testing.T) {
	one, err := domain.NewConversation(9, 4)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	other, err := domain.NewConversation(4, 9)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	if one.AccountAID != other.AccountAID || one.AccountBID != other.AccountBID {
		t.Fatalf("pairs differ: %+v vs %+v", one, other)
	}
	if one.AccountAID != 4 || one.AccountBID != 9 {
		t.Fatalf("pair = %d,%d, want it ordered", one.AccountAID, one.AccountBID)
	}
	if _, err := domain.NewConversation(4, 4); !errors.Is(err, domain.ErrSelfConversation) {
		t.Fatalf("NewConversation with oneself = %v, want ErrSelfConversation", err)
	}
}

// A read mark only moves forward: a client replaying an old request must not un-read a
// thread.
func TestConversation_MarkReadNeverGoesBackwards(t *testing.T) {
	c, err := domain.NewConversation(4, 9)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	now := c.LastMessageAt
	if err := c.MarkRead(4, now); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := c.MarkRead(4, now.Add(-time.Hour)); err != nil {
		t.Fatalf("MarkRead backwards: %v", err)
	}
	if mark := c.ReadMark(4); mark == nil || !mark.Equal(now) {
		t.Fatalf("mark = %v, want it left at the later time", mark)
	}
	// The other side's mark is what a read receipt compares against, and it is separate.
	if c.CounterpartyReadMark(4) != nil {
		t.Error("the counterparty's mark moved too")
	}
	if err := c.MarkRead(99, now); !errors.Is(err, domain.ErrNotAParticipant) {
		t.Fatalf("MarkRead by a stranger = %v, want ErrNotAParticipant", err)
	}
}

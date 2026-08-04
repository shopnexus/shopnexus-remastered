package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Conversation is the thread a pair of accounts shares, whoever happens to be buying.
// The pair is stored ordered, which is what makes the uniqueness impossible to sidestep
// by swapping sides and also rules out a thread with oneself.
//
// Not scoped to a listing: a product is referenced per message instead, which is how an
// offer card appears in a thread the two already had.
type Conversation struct {
	ID int64
	// Kind is what this thread is: two accounts talking, or a ticket's. A ticket thread pairs the
	// requester with the support desk's own account, so the moderator answering stays anonymous
	// and the next one inherits the thread rather than starting another.
	Kind string `validate:"required,oneof=direct ticket"`
	// TicketID is the ticket this thread belongs to, and nil on a direct one. It is also what makes
	// creating a ticket's thread idempotent — a retry finds the same row.
	TicketID      *int64
	AccountAID    int64 `validate:"required"`
	AccountBID    int64 `validate:"required"`
	LastMessageAt time.Time
	// The read marks. One timestamp per side rather than a status per message: marking a
	// thread read is one UPDATE of one row, and a read receipt is a comparison.
	AccountAReadAt *time.Time
	AccountBReadAt *time.Time
	CreatedAt      time.Time
}

// The two kinds of thread (kebab-case, mirrors the conversation_kind enum).
const (
	KindDirect = "direct"
	KindTicket = "ticket"
)

// NewConversation orders the pair, so the same two accounts always produce the same row
// whichever of them starts it.
func NewConversation(one, other int64) (Conversation, error) {
	if one == other {
		return Conversation{}, ErrSelfConversation
	}
	a, b := one, other
	if a > b {
		a, b = b, a
	}
	c := Conversation{Kind: KindDirect, AccountAID: a, AccountBID: b, LastMessageAt: time.Now()}
	if err := validation.Default().Struct(c); err != nil {
		return Conversation{}, validation.AsError(err)
	}
	return c, nil
}

// NewTicketThread is the thread behind a ticket: the requester and the support desk's own account.
// Ordered like any pair, because nothing downstream cares which side is which — Involves, the read
// marks and the counterparty all work off the two columns, which is the whole reason a ticket reuses
// this table instead of growing a nullable side.
func NewTicketThread(requesterID, deskID, ticketID int64) (Conversation, error) {
	c, err := NewConversation(requesterID, deskID)
	if err != nil {
		return Conversation{}, err
	}
	c.Kind = KindTicket
	c.TicketID = &ticketID
	if err := validation.Default().Struct(c); err != nil {
		return Conversation{}, validation.AsError(err)
	}
	return c, nil
}

// Ticket reports whether this is a support thread, which is what makes the other side answer as the
// desk rather than as whoever is on shift.
func (c Conversation) Ticket() bool { return c.Kind == KindTicket }

// Involves reports whether the account is one of the two sides — which is the whole of
// "may they read this thread".
func (c Conversation) Involves(accountID int64) bool {
	return accountID != 0 && (c.AccountAID == accountID || c.AccountBID == accountID)
}

// Counterparty is the other side, from one participant's point of view.
func (c Conversation) Counterparty(accountID int64) int64 {
	if c.AccountAID == accountID {
		return c.AccountBID
	}
	return c.AccountAID
}

// Other is the participant who is not actorID, or 0 when actorID is not in this
// conversation at all — a moderator acting on a report, say. Realtime addressing needs
// that distinction Counterparty does not make: sending to AccountAID for an outsider would
// notify a bystander of nothing they did.
func (c Conversation) Other(actorID int64) int64 {
	if !c.Involves(actorID) {
		return 0
	}
	return c.Counterparty(actorID)
}

// ReadMark is the caller's own mark, and CounterpartyReadMark the other side's — which is
// what a read receipt compares a message's time against.
func (c Conversation) ReadMark(accountID int64) *time.Time {
	if c.AccountAID == accountID {
		return c.AccountAReadAt
	}
	return c.AccountBReadAt
}

func (c Conversation) CounterpartyReadMark(accountID int64) *time.Time {
	if c.AccountAID == accountID {
		return c.AccountBReadAt
	}
	return c.AccountAReadAt
}

// MarkRead moves one side's mark forward. Never backwards: a client replaying an old
// request must not un-read a thread.
func (c *Conversation) MarkRead(accountID int64, at time.Time) error {
	if !c.Involves(accountID) {
		return ErrNotAParticipant
	}
	current := c.ReadMark(accountID)
	if current != nil && !at.After(*current) {
		return nil
	}
	if c.AccountAID == accountID {
		c.AccountAReadAt = &at
		return nil
	}
	c.AccountBReadAt = &at
	return nil
}

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/shared/id"
)

// The chat, and the price cards in it.
//
// A message carries its references in one JSONB envelope with two halves: "refs" is what the
// sender pointed at — a listing, an order — and "card" is what the backend asks the client to
// render. The client reads exactly two keys out of it, `card.offer_id` and `refs.listing_id`,
// and both of them are the *opaque wire id*, not the database key. That is why this command
// installs the id cipher at startup: a card holding a raw bigint renders as
// "Không thể tải đề nghị giá".

// writeChat lands every conversation and message, direct and ticket alike, and returns the
// conversation opened for each ticket so trust can point its row back at it.
func writeChat(
	ctx context.Context, pool *pgxpool.Pool, p *plan,
	parties map[string]party, cat catalogIDs, sales salesResult,
	deskID int64, ticketIDs map[string]int64,
) (map[string]int64, error) {
	threadOf := make(map[string]int64, len(p.tickets))

	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, t := range p.threads {
			a, ok := parties[t.a]
			if !ok {
				return fmt.Errorf("thread: no such account %q", t.a)
			}
			b, ok := parties[t.b]
			if !ok {
				return fmt.Errorf("thread: no such account %q", t.b)
			}
			convoID, err := writeConversation(ctx, tx, "direct", a.id, b.id, 0, firstAt(t.messages))
			if err != nil {
				return err
			}
			if err := writeMessages(ctx, tx, convoID, t.messages, parties, cat, sales); err != nil {
				return err
			}
		}

		for _, t := range p.tickets {
			requester, ok := parties[t.requester]
			if !ok {
				return fmt.Errorf("ticket %s: no such requester %q", t.key, t.requester)
			}
			ticketID, ok := ticketIDs[t.key]
			if !ok {
				return fmt.Errorf("ticket %s: not written", t.key)
			}
			convoID, err := writeConversation(ctx, tx, "ticket", requester.id, deskID, ticketID, t.createdAt)
			if err != nil {
				return err
			}
			if err := writeMessages(ctx, tx, convoID, t.messages, parties, cat, sales); err != nil {
				return err
			}
			threadOf[t.key] = convoID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return threadOf, nil
}

// writeConversation orders the pair by id, which "conversation_pair_ordered" requires and which
// is also what stops the same two people getting two threads by swapping sides. Nothing
// downstream cares which of the two ended up on side A.
func writeConversation(ctx context.Context, tx pgx.Tx, kind string, x, y, ticketID int64, at time.Time) (int64, error) {
	if x == y {
		return 0, fmt.Errorf("conversation with oneself (account %d)", x)
	}
	a, b := x, y
	if a > b {
		a, b = b, a
	}
	const q = `
		INSERT INTO conversation (kind, ticket_id, account_a_id, account_b_id,
		                          last_message_at, created_at)
		VALUES (@kind, @ticket_id, @a, @b, @at, @at)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"kind": kind, "ticket_id": dbx.NullID(ticketID), "a": a, "b": b, "at": at,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert conversation: %w", err)
	}
	return id, nil
}

func writeMessages(
	ctx context.Context, tx pgx.Tx, convoID int64, msgs []messagePlan,
	parties map[string]party, cat catalogIDs, sales salesResult,
) error {
	var last time.Time
	for _, m := range msgs {
		refs := map[string]any{}
		if m.listing > 0 {
			refs["listing_id"] = id.Of[id.Listing](cat.listings[m.listing-1]).String()
		}
		card := map[string]any{}
		body := m.body
		var senderID any
		msgType := "user"

		switch {
		case m.offerKey != "":
			// An offer card is a system row: it belongs to no participant, and the client
			// renders the card rather than the body. The body is still the service's own
			// wording, so a client that cannot resolve the card shows something truthful.
			offerID, ok := sales.offerIDs[m.offerKey]
			if !ok {
				return fmt.Errorf("message references unknown offer %q", m.offerKey)
			}
			card["offer_id"] = id.Of[id.Offer](offerID).String()
			msgType = "system"
			if body == "" {
				body = "offer opened"
			}
		default:
			sender, ok := parties[m.from]
			if !ok {
				return fmt.Errorf("message from unknown account %q", m.from)
			}
			senderID = sender.id
		}

		const q = `
			INSERT INTO message (conversation_id, sender_id, type, body, attachments,
			                     metadata, created_at)
			VALUES (@conversation_id, @sender_id, @type, @body, '{}', @metadata, @created_at)`
		envelope := map[string]any{}
		if len(refs) > 0 {
			envelope["refs"] = refs
		}
		if len(card) > 0 {
			envelope["card"] = card
		}
		_, err := tx.Exec(ctx, q, pgx.NamedArgs{
			"conversation_id": convoID,
			"sender_id":       senderID,
			"type":            msgType,
			"body":            body,
			"metadata":        dbx.JSONObject(envelope),
			"created_at":      m.at,
		})
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
		if m.at.After(last) {
			last = m.at
		}
	}
	if last.IsZero() {
		return nil
	}
	// The inbox sorts on this and nothing maintains it but the writer. A thread whose newest
	// message is a week newer than its "last_message_at" sinks to the bottom of the list it
	// should be at the top of.
	const bump = `UPDATE conversation SET last_message_at = @at WHERE id = @id`
	if _, err := tx.Exec(ctx, bump, pgx.NamedArgs{"at": last, "id": convoID}); err != nil {
		return fmt.Errorf("update conversation timestamp: %w", err)
	}
	return nil
}

func firstAt(msgs []messagePlan) time.Time {
	if len(msgs) == 0 {
		return time.Now()
	}
	first := msgs[0].at
	for _, m := range msgs[1:] {
		if m.at.Before(first) {
			first = m.at
		}
	}
	return first
}

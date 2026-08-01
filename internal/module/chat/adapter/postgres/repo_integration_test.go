//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/infra/postgres"
	chatpg "shopnexus/internal/module/chat/adapter/postgres"
	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CHAT_DB_DSN")
	if dsn == "" {
		t.Skip("CHAT_DB_DSN not set")
	}
	return dsn
}

func newRepo(t *testing.T) *chatpg.Repo {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "chat")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return chatpg.New(pool)
}

// pair keeps one test's thread out of another's: the schema is shared across runs, and a
// fixed pair would make the unread counts depend on history.
func pair(t *testing.T) (int64, int64) {
	t.Helper()
	base := time.Now().UnixNano() % 1_000_000_000
	return base, base + 1
}

// One thread per ordered pair, whichever side asks — which is what the unique constraint
// on the ordered columns buys, and the reason EnsureConversation is an upsert.
func TestRepo_EnsureConversationIsOnePerPair(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	alice, bob := pair(t)

	first, err := repo.EnsureConversation(ctx, alice, bob)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	// From the other side: the pair is stored ordered, so this cannot make a second.
	again, err := repo.EnsureConversation(ctx, bob, alice)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if first.ID != again.ID {
		t.Fatalf("threads differ: %d vs %d", first.ID, again.ID)
	}
	if first.AccountAID != min(alice, bob) {
		t.Fatalf("pair = %d,%d, want it ordered", first.AccountAID, first.AccountBID)
	}
	if _, err := repo.EnsureConversation(ctx, alice, alice); !errors.Is(err, domain.ErrSelfConversation) {
		t.Fatalf("a thread with oneself = %v", err)
	}
}

// A message moves the thread's last_message_at in the same transaction, which is what the
// inbox is ordered by — and the unread count is the counterparty's messages after the
// caller's own mark.
func TestRepo_MessagesDriveTheInboxAndUnread(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	alice, bob := pair(t)
	thread, err := repo.EnsureConversation(ctx, alice, bob)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}

	for _, body := range []string{"hello", "still there?"} {
		m, err := domain.NewMessage(thread.ID, alice, body, nil, map[string]any{"listing_id": 7})
		if err != nil {
			t.Fatalf("NewMessage: %v", err)
		}
		if err := repo.InsertMessage(ctx, &m); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	// The thread's timestamp followed the message, so the inbox sorts on it.
	inbox, err := repo.ListConversations(ctx, port.InboxFilter{AccountID: bob, Limit: 10})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(inbox) == 0 || inbox[0].ID != thread.ID {
		t.Fatalf("inbox = %+v, want the thread", inbox)
	}
	if !inbox[0].LastMessageAt.After(thread.CreatedAt) {
		t.Error("last_message_at did not follow the message")
	}

	counts, err := repo.UnreadCounts(ctx, bob, []int64{thread.ID})
	if err != nil {
		t.Fatalf("UnreadCounts: %v", err)
	}
	if counts[thread.ID] != 2 {
		t.Fatalf("unread for the recipient = %d, want 2", counts[thread.ID])
	}
	// The sender's own messages are not unread for the sender.
	counts, err = repo.UnreadCounts(ctx, alice, []int64{thread.ID})
	if err != nil {
		t.Fatalf("UnreadCounts: %v", err)
	}
	if counts[thread.ID] != 0 {
		t.Fatalf("unread for the sender = %d, want 0", counts[thread.ID])
	}

	last, err := repo.LastMessages(ctx, []int64{thread.ID})
	if err != nil {
		t.Fatalf("LastMessages: %v", err)
	}
	if last[thread.ID].Body != "still there?" {
		t.Fatalf("last message = %+v", last[thread.ID])
	}
	// The refs the sender attached survive the round trip through the metadata column.
	if last[thread.ID].Refs["listing_id"] == nil {
		t.Errorf("refs = %+v, want the listing reference", last[thread.ID].Refs)
	}
}

// The read mark only moves forward, in SQL as well as in the domain: GREATEST is what
// stops a replayed request from un-reading a thread.
func TestRepo_ReadMarkNeverGoesBackwards(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	alice, bob := pair(t)
	thread, err := repo.EnsureConversation(ctx, alice, bob)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}

	now := time.Now()
	if err := thread.MarkRead(alice, now); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := repo.SaveConversation(ctx, thread); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	// An older mark, as a retried request would carry.
	stale := thread
	stale.AccountAReadAt = new(now.Add(-time.Hour))
	if err := repo.SaveConversation(ctx, stale); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	stored, err := repo.FindConversation(ctx, thread.ID)
	if err != nil {
		t.Fatalf("FindConversation: %v", err)
	}
	if stored.AccountAReadAt == nil || stored.AccountAReadAt.Before(now.Add(-time.Minute)) {
		t.Fatalf("mark = %v, want it left at the later time", stored.AccountAReadAt)
	}
}

// A system message has no sender, which the column holds as NULL — and an edit or a
// redaction is keyed by id and created_at, because the hypertable's key includes both.
func TestRepo_SystemMessageAndRedaction(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	alice, bob := pair(t)
	thread, err := repo.EnsureConversation(ctx, alice, bob)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}

	card, err := domain.NewSystemMessage(thread.ID, "offer updated", map[string]any{"offer_id": 42})
	if err != nil {
		t.Fatalf("NewSystemMessage: %v", err)
	}
	if err := repo.InsertMessage(ctx, &card); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	stored, err := repo.FindMessage(ctx, card.ID)
	if err != nil {
		t.Fatalf("FindMessage: %v", err)
	}
	if stored.SenderID != 0 || stored.Type != domain.TypeSystem {
		t.Fatalf("message = %+v, want a senderless system row", stored)
	}
	if stored.Card["offer_id"] == nil {
		t.Fatalf("card = %+v, want the offer reference", stored.Card)
	}

	m, err := domain.NewMessage(thread.ID, alice, "oops", nil, nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := repo.InsertMessage(ctx, &m); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := m.Redact(alice, false); err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if err := repo.SaveMessage(ctx, m); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	after, err := repo.FindMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("FindMessage: %v", err)
	}
	// The row survives so the thread has no unexplained gaps; the content does not.
	if after.Body != "" || after.IsLive() {
		t.Fatalf("message = %+v, want it emptied and marked deleted", after)
	}
	// The redacted one stops counting — there is nothing left to read. The system card
	// still does: it has no sender, so it is nobody's own and is news to both sides.
	counts, err := repo.UnreadCounts(ctx, bob, []int64{thread.ID})
	if err != nil {
		t.Fatalf("UnreadCounts: %v", err)
	}
	if counts[thread.ID] != 1 {
		t.Fatalf("unread = %d, want only the system card", counts[thread.ID])
	}
	// And for the sender of the redacted message, the card is unread too.
	counts, err = repo.UnreadCounts(ctx, alice, []int64{thread.ID})
	if err != nil {
		t.Fatalf("UnreadCounts: %v", err)
	}
	if counts[thread.ID] != 1 {
		t.Fatalf("unread for the sender = %d, want the system card", counts[thread.ID])
	}
}

// CURRENT_TIMESTAMP is transaction-scoped, so two messages a real transaction writes can
// share created_at exactly. The cursor has to be the (created_at, id) tuple, or a page
// boundary landing on that tie drops whichever row page N did not return.
func TestRepo_ListMessagesCursorSurvivesATimestampTie(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	alice, bob := pair(t)
	thread, err := repo.EnsureConversation(ctx, alice, bob)
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}

	var ids []int64
	for _, body := range []string{"one", "two"} {
		m, err := domain.NewMessage(thread.ID, alice, body, nil, nil)
		if err != nil {
			t.Fatalf("NewMessage: %v", err)
		}
		if err := repo.InsertMessage(ctx, &m); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
		ids = append(ids, m.ID)
	}

	// Force the tie a same-transaction write produces in production: the hypertable
	// allows moving a row's created_at, which is exactly what this simulates.
	pool, err := postgres.NewPool(ctx, testDSN(t), "chat")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	const tie = `UPDATE message SET created_at = (SELECT created_at FROM message WHERE id = @first) WHERE id = @second`
	if _, err := pool.Exec(ctx, tie, pgx.NamedArgs{"first": ids[0], "second": ids[1]}); err != nil {
		t.Fatalf("force tie: %v", err)
	}

	page, err := repo.ListMessages(ctx, port.HistoryFilter{ConversationID: thread.ID, Limit: 1})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page 1 = %+v, want one row", page)
	}
	next, err := repo.ListMessages(ctx, port.HistoryFilter{
		ConversationID: thread.ID, Before: page[0].CreatedAt, BeforeID: page[0].ID, Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListMessages(page 2): %v", err)
	}
	if len(next) != 1 || next[0].ID == page[0].ID {
		t.Fatalf("page 2 = %+v, want the tied message rather than being skipped", next)
	}
}

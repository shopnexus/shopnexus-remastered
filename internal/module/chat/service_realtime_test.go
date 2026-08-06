package chat_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"shopnexus/internal/module/chat"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/realtime"
	"shopnexus/internal/shared/validation"
)

// conversationBetween seeds a fresh fake repo with the one thread these tests exercise —
// the way a real database already holds a thread before a command runs. It is the repo's
// only conversation, so every request helper below addresses conversation id 1.
func conversationBetween(one, other int64) *fakeRepo {
	repo := newFakeRepo()
	if _, err := repo.EnsureConversation(context.Background(), one, other); err != nil {
		panic("seed conversation: " + err.Error())
	}
	return repo
}

const testConversationID = 1

// newTestServiceWithFanout is newHarness's constructor plus the one dependency this task
// adds — a fanout, so a test can assert on what a command pushed.
func newTestServiceWithFanout(t *testing.T, fanout realtime.Fanout, repo *fakeRepo) *chat.Service {
	t.Helper()
	return chat.NewService(repo, fakeAccounts{}, newFakeUploads(), validation.Default(), slog.New(slog.DiscardHandler), fanout)
}

func sendMessageRequest(actorID int64, body string) chatapi.SendMessageRequest {
	return chatapi.SendMessageRequest{
		ActorID:        id.Of[id.Account](actorID),
		ConversationID: id.Of[id.Conversation](testConversationID),
		Body:           body,
	}
}

// recorder captures what the service pushed, so a test asserts on recipients and codes
// without a bus.
type recorder struct {
	mu   sync.Mutex
	sent []recorded
}

type recorded struct {
	subject string
	env     realtime.Envelope
}

func (r *recorder) Broadcast(subject string, b []byte) error {
	var env realtime.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, recorded{subject: subject, env: env})
	return nil
}

func (r *recorder) OnBroadcast(string, func([]byte)) (func(), error) { return func() {}, nil }

func (r *recorder) codes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.sent))
	for _, s := range r.sent {
		out = append(out, s.env.Code)
	}
	return out
}

// The sender must not receive their own message: they already have it, and a second copy
// arriving asynchronously duplicates the optimistic row.
func TestSendMessageNotifiesOnlyTheOtherParticipant(t *testing.T) {
	const sender, recipient int64 = 42, 77

	rec := &recorder{}
	svc := newTestServiceWithFanout(t, rec, conversationBetween(sender, recipient))

	_, err := svc.SendMessage(t.Context(), sendMessageRequest(sender, "hello"))
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if got := rec.codes(); len(got) != 1 || got[0] != "chat.message_created" {
		t.Fatalf("codes = %v, want [chat.message_created]", got)
	}
	if got, want := rec.sent[0].subject, realtime.AccountSubject(recipient); got != want {
		t.Errorf("subject = %q, want %q — the recipient, not the sender", got, want)
	}

	var msg chatapi.Message
	if err := json.Unmarshal(rec.sent[0].env.Data, &msg); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if msg.Body != "hello" {
		t.Errorf("body = %q, want hello", msg.Body)
	}
}

// A push that fails must not fail the write: the row is already committed, so the caller
// gets their 201 and the interface is briefly stale.
func TestSendMessageSucceedsWhenTheBusIsDown(t *testing.T) {
	svc := newTestServiceWithFanout(t, failingFanout{}, conversationBetween(42, 77))

	if _, err := svc.SendMessage(t.Context(), sendMessageRequest(42, "hello")); err != nil {
		t.Fatalf("SendMessage: %v — a realtime failure must not fail the write", err)
	}
}

type failingFanout struct{}

func (failingFanout) Broadcast(string, []byte) error { return errors.New("nats down") }
func (failingFanout) OnBroadcast(string, func([]byte)) (func(), error) {
	return func() {}, nil
}

func TestMarkReadNotifiesTheOtherParticipant(t *testing.T) {
	const reader, other int64 = 42, 77

	rec := &recorder{}
	svc := newTestServiceWithFanout(t, rec, conversationBetween(reader, other))

	if _, err := svc.MarkConversationRead(t.Context(), chatapi.MarkConversationReadRequest{
		ActorID: id.Of[id.Account](reader), ID: id.Of[id.Conversation](testConversationID),
	}); err != nil {
		t.Fatalf("MarkConversationRead: %v", err)
	}

	if got := rec.codes(); len(got) != 1 || got[0] != "chat.conversation_read" {
		t.Fatalf("codes = %v, want [chat.conversation_read]", got)
	}
	if got, want := rec.sent[0].subject, realtime.AccountSubject(other); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}

	var mark chatapi.ConversationReadMark
	if err := json.Unmarshal(rec.sent[0].env.Data, &mark); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if mark.ReaderID.Int64() != reader {
		t.Errorf("reader_id = %d, want %d", mark.ReaderID.Int64(), reader)
	}
}

// The deletion event carries enough to find the row without a body: a redacted message has
// none, and sending an emptied Message would read as an edit rather than a removal.
func TestDeleteMessageNotifiesWithARef(t *testing.T) {
	const sender, recipient int64 = 42, 77

	repo := conversationBetween(sender, recipient)
	// Seeded on a quiet fanout: this test is about the redaction's own notification, not
	// the send's.
	seed := newTestServiceWithFanout(t, failingFanout{}, repo)
	sent, err := seed.SendMessage(t.Context(), sendMessageRequest(sender, "hello"))
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	rec := &recorder{}
	svc := newTestServiceWithFanout(t, rec, repo)
	if err := svc.RedactMessage(t.Context(), chatapi.RedactMessageRequest{
		ActorID: id.Of[id.Account](sender), ID: sent.ID, CreatedAt: sent.CreatedAt,
	}); err != nil {
		t.Fatalf("RedactMessage: %v", err)
	}

	if got := rec.codes(); len(got) != 1 || got[0] != "chat.message_deleted" {
		t.Fatalf("codes = %v, want [chat.message_deleted]", got)
	}
	var ref chatapi.DeletedMessageRef
	if err := json.Unmarshal(rec.sent[0].env.Data, &ref); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if ref.CreatedAt.IsZero() {
		t.Error("created_at is zero; the client cannot locate the row without it")
	}
	if ref.ID != sent.ID {
		t.Errorf("id = %v, want %v", ref.ID, sent.ID)
	}
}

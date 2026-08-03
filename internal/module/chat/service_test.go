package chat_test

import (
	"context"
	"log/slog"
	"testing"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	"shopnexus/internal/module/chat"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// fakeAccounts answers the one question chat asks of the account module: what the
// counterparty is called, which is what an inbox row shows.
type fakeAccounts struct{ accounttest.Stub }

func (fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	return accountapi.PublicAccount{ID: req.ID, Name: "Somebody"}, nil
}

type harness struct {
	svc     *chat.Service
	repo    *fakeRepo
	uploads *fakeUploads
}

func newHarness() *harness {
	repo := newFakeRepo()
	uploads := newFakeUploads()
	svc := chat.NewService(repo, fakeAccounts{}, uploads, validation.Default(), slog.New(slog.DiscardHandler), noopFanout{})
	return &harness{svc: svc, repo: repo, uploads: uploads}
}

// noopFanout is the fanout for tests that are not about realtime: it accepts every push
// and drops it, so a command's happy path does not need a bus to run.
type noopFanout struct{}

func (noopFanout) Broadcast(string, []byte) error                   { return nil }
func (noopFanout) OnBroadcast(string, func([]byte)) (func(), error) { return func() {}, nil }

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func mustErr[T any](_ T, err error) error { return err }

const (
	alice = id.ID[id.Account](1)
	bob   = id.ID[id.Account](2)
	carol = id.ID[id.Account](3)
)

// One thread per pair, whoever opens it: the second call answers the first's thread
// rather than making another.
func TestStartConversation_IsIdempotent(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	first, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	// From the other side, which is the case the ordered pair exists to handle.
	again, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: bob, AccountID: alice})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if first.ID != again.ID {
		t.Fatalf("threads differ: %v vs %v", first.ID, again.ID)
	}
	// Each side sees the other as the counterparty.
	if first.Counterparty.ID != bob || again.Counterparty.ID != alice {
		t.Fatalf("counterparties = %v, %v", first.Counterparty.ID, again.Counterparty.ID)
	}
	if _, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{
		ActorID: alice, AccountID: alice,
	}); status(t, err) != 422 {
		t.Error("a thread with oneself was accepted")
	}
}

// The unread badge is the counterparty's messages after the caller's own mark — so
// sending does not make your own thread unread, and reading clears it.
func TestUnread_CountsTheOtherSideOnly(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	for _, body := range []string{"hello", "are you there"} {
		if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
			ActorID: alice, ConversationID: thread.ID, Body: body,
		}); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}

	// Alice sent them, so nothing is unread for her and two are for Bob.
	mine, err := h.svc.GetUnreadCount(ctx, chatapi.UnreadCountRequest{ActorID: alice})
	if err != nil {
		t.Fatalf("GetUnreadCount: %v", err)
	}
	if mine.Unread != 0 {
		t.Errorf("sender's unread = %d, want 0", mine.Unread)
	}
	theirs, err := h.svc.GetUnreadCount(ctx, chatapi.UnreadCountRequest{ActorID: bob})
	if err != nil {
		t.Fatalf("GetUnreadCount: %v", err)
	}
	if theirs.Unread != 2 || theirs.Conversations != 1 {
		t.Fatalf("recipient's badge = %+v, want 2 in 1 thread", theirs)
	}

	if _, err := h.svc.MarkConversationRead(ctx, chatapi.MarkConversationReadRequest{
		ActorID: bob, ID: thread.ID,
	}); err != nil {
		t.Fatalf("MarkConversationRead: %v", err)
	}
	theirs, err = h.svc.GetUnreadCount(ctx, chatapi.UnreadCountRequest{ActorID: bob})
	if err != nil {
		t.Fatalf("GetUnreadCount: %v", err)
	}
	if theirs.Unread != 0 {
		t.Fatalf("badge after reading = %+v, want it cleared", theirs)
	}
}

// A thread the caller is not in is not found rather than forbidden: it is not theirs to
// know about, and a 403 would confirm it exists.
func TestConversation_StrangerSeesNothing(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	for _, err := range []error{
		mustErr(h.svc.GetConversation(ctx, chatapi.GetConversationRequest{ActorID: carol, ID: thread.ID})),
		mustErr(h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: carol, ID: thread.ID, Limit: 20})),
		mustErr(h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
			ActorID: carol, ConversationID: thread.ID, Body: "butting in",
		})),
	} {
		if got := status(t, err); got != 404 {
			t.Errorf("status = %d, want 404", got)
		}
	}
}

// A system message is the backend's word, and a client can neither forge nor change one.
func TestPostSystemMessage_IsNotAUsers(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	card, err := h.svc.PostSystemMessage(ctx, chatapi.PostSystemMessageRequest{
		AccountAID: alice, AccountBID: bob, Body: "offer updated",
		Card: map[string]any{"offer_id": "ofr_1"},
	})
	if err != nil {
		t.Fatalf("PostSystemMessage: %v", err)
	}
	if card.SenderID != nil || card.Type != domain.TypeSystem {
		t.Fatalf("message = %+v, want a senderless system message", card)
	}
	// The card carries the offer's id and nothing else: the terms live on the offer, so a
	// counter-offer cannot leave an old price on screen.
	if card.Card["offer_id"] != "ofr_1" {
		t.Fatalf("card = %+v", card.Card)
	}
	// It opened the thread, so the pair now has one without anybody calling for it.
	inbox, err := h.svc.ListConversations(ctx, chatapi.ListConversationsRequest{ActorID: alice, Limit: 20})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(inbox.Data) != 1 {
		t.Fatalf("inbox = %+v, want the opened thread", inbox.Data)
	}

	if err := mustErr(h.svc.UpdateMessage(ctx, chatapi.UpdateMessageRequest{
		ActorID: alice, ID: card.ID, Body: "forged", CreatedAt: card.CreatedAt,
	})); status(t, err) != 403 {
		t.Error("a user edited a system message")
	}
}

// Editing and unsending are the sender's own, and a redaction keeps the row so the thread
// has no unexplained gaps.
func TestMessage_EditAndRedact(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	sent, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "hello",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if err := mustErr(h.svc.UpdateMessage(ctx, chatapi.UpdateMessageRequest{
		ActorID: bob, ID: sent.ID, Body: "not mine", CreatedAt: sent.CreatedAt,
	})); status(t, err) != 403 {
		t.Error("somebody else edited the message")
	}
	edited, err := h.svc.UpdateMessage(ctx, chatapi.UpdateMessageRequest{
		ActorID: alice, ID: sent.ID, Body: "hello again", CreatedAt: sent.CreatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	if edited.Body != "hello again" || edited.EditedAt == nil {
		t.Fatalf("message = %+v, want it edited and marked", edited)
	}

	if err := h.svc.RedactMessage(ctx, chatapi.RedactMessageRequest{
		ActorID: alice, ID: sent.ID, CreatedAt: sent.CreatedAt,
	}); err != nil {
		t.Fatalf("RedactMessage: %v", err)
	}
	page, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{
		ActorID: alice, ID: thread.ID, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("history = %+v, want the redacted row kept", page.Data)
	}
	if page.Data[0].Body != "" || page.Data[0].DeletedAt == nil {
		t.Fatalf("message = %+v, want it emptied and marked", page.Data[0])
	}
	// And a redacted message stops counting as unread, because there is nothing to read.
	badge, err := h.svc.GetUnreadCount(ctx, chatapi.UnreadCountRequest{ActorID: bob})
	if err != nil {
		t.Fatalf("GetUnreadCount: %v", err)
	}
	if badge.Unread != 0 {
		t.Fatalf("badge = %+v, want a redacted message not to count", badge)
	}
}

// CURRENT_TIMESTAMP is transaction-scoped, so two messages written in one transaction can
// share created_at exactly. A bare-timestamp cursor would then exclude whichever one page
// N did not return; the (created_at, id) tuple cursor must not.
func TestListMessages_CursorDoesNotSkipATimestampTie(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	first, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "one",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	second, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "two",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// Force the tie the real database produces for a same-transaction write.
	for i := range h.repo.messages {
		if h.repo.messages[i].ID == second.ID.Int64() {
			h.repo.messages[i].CreatedAt = first.CreatedAt
		}
	}

	page, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: alice, ID: thread.ID, Limit: 1})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(page.Data) != 1 || !page.Meta.HasMore {
		t.Fatalf("page 1 = %+v, want one row and another page", page)
	}
	next, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{
		ActorID: alice, ID: thread.ID, Cursor: page.Meta.NextCursor, Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListMessages(page 2): %v", err)
	}
	if len(next.Data) != 1 {
		t.Fatalf("page 2 = %+v, want the tied message rather than being skipped", next.Data)
	}
}

// An attachment has to be one of this module's own confirmed uploads: a message pointing
// at nothing is a photo that never renders.
func TestSendMessage_UnknownAttachment(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	err = mustErr(h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID,
		Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}))
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}

	// Declared as confirmed, a photo with no caption goes through: an attachment is
	// something to say.
	h.uploads.confirmed[42] = true
	if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID,
		Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("SendMessage with an attachment: %v", err)
	}
}

// A message with neither text nor an attachment says nothing.
func TestSendMessage_NeedsSomethingToSay(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	err = mustErr(h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID,
	}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

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

// fakeAccounts answers what chat asks of the account module: what a counterparty is called,
// which account is the support desk, and whether the caller is staff.
type fakeAccounts struct {
	accounttest.Stub
	// roles is per account, so one harness serves a moderator and an ordinary user.
	roles map[id.ID[id.Account]]string
}

func (fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	return accountapi.PublicAccount{ID: req.ID, Name: "Somebody"}, nil
}

func (f fakeAccounts) GetMe(_ context.Context, req accountapi.GetMeRequest) (accountapi.Me, error) {
	role := f.roles[req.ActorID]
	if role == "" {
		role = "user"
	}
	return accountapi.Me{Role: role}, nil
}

func (fakeAccounts) GetSupportAccount(context.Context) (accountapi.AccountSummary, error) {
	return accountapi.AccountSummary{ID: desk, Name: "Hỗ trợ ShopNexus"}, nil
}

type harness struct {
	svc     *chat.Service
	repo    *fakeRepo
	uploads *fakeUploads
}

// newFakeAccounts is the account module as chat sees it, with one moderator on shift — the role
// that lets staff into a ticket thread they are not a side of.
func newFakeAccounts() fakeAccounts {
	return fakeAccounts{roles: map[id.ID[id.Account]]string{moderator: "moderator"}}
}

func newHarness() *harness {
	repo := newFakeRepo()
	uploads := newFakeUploads()
	svc := chat.NewService(repo, newFakeAccounts(), uploads, validation.Default(), slog.New(slog.DiscardHandler), noopFanout{})
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
	alice     = id.ID[id.Account](1)
	bob       = id.ID[id.Account](2)
	carol     = id.ID[id.Account](3)
	moderator = id.ID[id.Account](4)
	desk      = id.ID[id.Account](5)
	ticketID  = id.ID[id.Ticket](60)
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

// A ticket's thread: the requester on one side and the desk's own account on the other, which is
// what makes support anonymous and lets the next moderator inherit the thread. Opening it twice is
// the repair path trust relies on, so it answers the same thread and posts the opening line once.
func TestTicketThread_AnonymousDeskAndOneOpeningMessage(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	open := chatapi.OpenTicketThreadRequest{RequesterID: alice, TicketID: ticketID, Body: "đơn của tôi bị lỗi"}
	thread, err := h.svc.OpenTicketThread(ctx, open)
	if err != nil {
		t.Fatalf("OpenTicketThread: %v", err)
	}
	if thread.TicketID == nil || *thread.TicketID != ticketID {
		t.Fatalf("thread = %+v, want it marked as the ticket's", thread)
	}
	again, err := h.svc.OpenTicketThread(ctx, open)
	if err != nil {
		t.Fatalf("second OpenTicketThread: %v", err)
	}
	if again.ID != thread.ID {
		t.Fatalf("threads differ: %v vs %v", thread.ID, again.ID)
	}

	// A moderator is let in without being a side of the row — that is how staff answer at all —
	// and writes as themselves.
	if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: moderator, ConversationID: thread.ID, Body: "chúng tôi đang kiểm tra",
	}); err != nil {
		t.Fatalf("moderator SendMessage: %v", err)
	}
	// A stranger still is not: a ticket id being guessable must not make the thread readable.
	if got := status(t, mustErr(h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{
		ActorID: carol, ID: thread.ID, Limit: 10,
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for somebody else's ticket thread", got)
	}

	mine, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{
		ActorID: alice, ID: thread.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(mine.Data) != 2 {
		t.Fatalf("messages = %d, want the opening line once plus the reply", len(mine.Data))
	}
	for _, m := range mine.Data {
		switch m.Body {
		case open.Body:
			if m.SenderID == nil || *m.SenderID != alice {
				t.Errorf("own message = %+v, want it attributed to the requester", m)
			}
		default:
			// The requester is told a reply came from support and nothing more: a decision is the
			// platform's, not a named person's to be argued with afterwards.
			if m.SenderID != nil || !m.FromSupport {
				t.Errorf("reply = %+v, want it anonymous to the requester", m)
			}
		}
	}
	// Staff read their own queue, where a colleague's name is what makes a thread reviewable.
	staff, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{
		ActorID: moderator, ID: thread.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListMessages as staff: %v", err)
	}
	for _, m := range staff.Data {
		if m.FromSupport {
			t.Errorf("message = %+v, want the real sender for staff", m)
		}
	}
}

// The requester's inbox is rendered from the same facts as the thread, so it has to hide the same
// things: a moderator's account id in `last_message.sender_id` is a name and a shop page away
// (`GET /accounts/{id}` needs no token), which is the whole point of answering as the desk.
func TestTicketThread_TheInboxRowHidesSupportToo(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.OpenTicketThread(ctx, chatapi.OpenTicketThreadRequest{
		RequesterID: alice, TicketID: ticketID, Body: "đơn của tôi bị lỗi",
	})
	if err != nil {
		t.Fatalf("OpenTicketThread: %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: moderator, ConversationID: thread.ID, Body: "chúng tôi đang kiểm tra",
	}); err != nil {
		t.Fatalf("moderator SendMessage: %v", err)
	}

	inbox, err := h.svc.ListConversations(ctx, chatapi.ListConversationsRequest{ActorID: alice, Limit: 10})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(inbox.Data) != 1 {
		t.Fatalf("inbox = %d rows, want the ticket thread", len(inbox.Data))
	}
	row := inbox.Data[0]
	if row.TicketID == nil || *row.TicketID != ticketID {
		t.Errorf("row = %+v, want it marked as a ticket's", row)
	}
	if row.Counterparty.ID != desk {
		t.Errorf("counterparty = %v, want the desk", row.Counterparty.ID)
	}
	if row.LastMessage == nil || row.LastMessage.SenderID != nil || !row.LastMessage.FromSupport {
		t.Fatalf("last message = %+v, want it anonymous to the requester", row.LastMessage)
	}
	// The single read is the same row, so it cannot answer differently.
	one, err := h.svc.GetConversation(ctx, chatapi.GetConversationRequest{ActorID: alice, ID: thread.ID})
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if one.LastMessage == nil || one.LastMessage.SenderID != nil || !one.LastMessage.FromSupport {
		t.Fatalf("last message = %+v, want it anonymous here as well", one.LastMessage)
	}
}

// Staff are not a side of a ticket thread, so every viewer-relative value is computed as the desk:
// otherwise the counterparty and the read marks fall through to whichever account id sorts lower,
// and marking the thread read is refused outright on a route staff call on opening it.
func TestTicketThread_StaffActAsTheDesk(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.OpenTicketThread(ctx, chatapi.OpenTicketThreadRequest{
		RequesterID: alice, TicketID: ticketID, Body: "đơn của tôi bị lỗi",
	})
	if err != nil {
		t.Fatalf("OpenTicketThread: %v", err)
	}

	staffView, err := h.svc.GetConversation(ctx, chatapi.GetConversationRequest{ActorID: moderator, ID: thread.ID})
	if err != nil {
		t.Fatalf("GetConversation as staff: %v", err)
	}
	if staffView.Counterparty.ID != alice {
		t.Errorf("counterparty = %v, want the requester", staffView.Counterparty.ID)
	}
	if staffView.Unread != 1 {
		t.Errorf("unread = %d, want the requester's opening message unread for the desk", staffView.Unread)
	}

	read, err := h.svc.MarkConversationRead(ctx, chatapi.MarkConversationReadRequest{ActorID: moderator, ID: thread.ID})
	if err != nil {
		t.Fatalf("MarkConversationRead as staff: %v", err)
	}
	if read.ReadAt == nil || read.Unread != 0 {
		t.Fatalf("row = %+v, want the desk's mark moved", read)
	}
	// Shared, not personal: the next moderator inherits it, and the requester's receipt says
	// support read the thread.
	next, err := h.svc.GetConversation(ctx, chatapi.GetConversationRequest{ActorID: moderator, ID: thread.ID})
	if err != nil {
		t.Fatalf("GetConversation as staff: %v", err)
	}
	if next.ReadAt == nil {
		t.Fatalf("row = %+v, want the mark to survive for the next moderator", next)
	}
	mine, err := h.svc.GetConversation(ctx, chatapi.GetConversationRequest{ActorID: alice, ID: thread.ID})
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if mine.CounterpartyReadAt == nil {
		t.Fatalf("row = %+v, want the requester told support read it", mine)
	}
}

// Support is reached by raising a ticket. The desk's id is public — it is the counterparty of every
// ticket thread — and a direct thread with it is one no moderator would ever read.
func TestStartConversation_RefusesTheSupportDesk(t *testing.T) {
	h := newHarness()
	if got := status(t, mustErr(h.svc.StartConversation(context.Background(), chatapi.StartConversationRequest{
		ActorID: alice, AccountID: desk,
	}))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A verdict decided in another module reaches the requester where they raised it, and it opens the
// thread if the ticket never got one — the same repair `OpenTicketThread` is.
func TestPostTicketMessage_LandsInTheThread(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	posted, err := h.svc.PostTicketMessage(ctx, chatapi.PostTicketMessageRequest{
		RequesterID: alice, TicketID: ticketID, Body: "hoàn tiền đã được chấp nhận",
		Card: map[string]any{"refund_id": "rfd_z3n8kvq1wd6pt"},
	})
	if err != nil {
		t.Fatalf("PostTicketMessage: %v", err)
	}
	if posted.SenderID != nil || posted.Type != domain.TypeSystem {
		t.Fatalf("message = %+v, want a system message", posted)
	}
	// Idempotent enough to be the repair path: the thread it opened is the one the ticket then uses.
	thread, err := h.svc.OpenTicketThread(ctx, chatapi.OpenTicketThreadRequest{
		RequesterID: alice, TicketID: ticketID, Body: "khiếu nại của tôi",
	})
	if err != nil {
		t.Fatalf("OpenTicketThread: %v", err)
	}
	if thread.ID != posted.ConversationID {
		t.Fatalf("thread = %v, want the one the verdict was posted into (%v)", thread.ID, posted.ConversationID)
	}
	// And the opening line is not written after the fact: the thread already had a message, so the
	// requester's words would arrive after the verdict that answered them.
	msgs, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: alice, ID: thread.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs.Data) != 1 {
		t.Fatalf("messages = %d, want only the verdict", len(msgs.Data))
	}
}

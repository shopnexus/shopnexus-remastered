package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/domain"
)

// A reply carries the quote resolved from the message it names, not a copy taken when it was
// sent — so the two halves of the reference travel and the preview is read live.
func TestSendMessage_ReplyCarriesTheQuote(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	asked, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "cái áo xanh còn không?",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	answered, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: bob, ConversationID: thread.ID, Body: "còn nhé",
		ReplyTo: &chatapi.MessageRefRequest{ID: asked.ID, CreatedAt: asked.CreatedAt},
	})
	if err != nil {
		t.Fatalf("SendMessage reply: %v", err)
	}
	if answered.ReplyTo == nil {
		t.Fatal("reply_to = nil, want the quoted message")
	}
	if answered.ReplyTo.ID != asked.ID {
		t.Errorf("reply_to.id = %v, want %v", answered.ReplyTo.ID, asked.ID)
	}
	if answered.ReplyTo.Preview != "cái áo xanh còn không?" {
		t.Errorf("reply_to.preview = %q, want the quoted body", answered.ReplyTo.Preview)
	}
	if answered.ReplyTo.SenderID == nil || *answered.ReplyTo.SenderID != alice {
		t.Errorf("reply_to.sender_id = %v, want %v", answered.ReplyTo.SenderID, alice)
	}

	// And again on the read path, which resolves the whole page in one go.
	page, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: alice, ID: thread.ID, Limit: 20})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var found bool
	for _, m := range page.Data {
		if m.ID != answered.ID {
			continue
		}
		found = true
		if m.ReplyTo == nil || m.ReplyTo.ID != asked.ID {
			t.Errorf("reply_to on the page = %+v, want the quoted message", m.ReplyTo)
		}
	}
	if !found {
		t.Fatal("the reply is missing from the thread")
	}
}

// The quote is a preview of what was said, so a target from another conversation would read
// a thread the sender is not in out through one they are.
func TestSendMessage_ReplyOutsideTheThreadIsRefused(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	ours, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	theirs, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: bob, AccountID: carol})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	elsewhere, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: carol, ConversationID: theirs.ID, Body: "private",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, err = h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: ours.ID, Body: "quoting somebody else's thread",
		ReplyTo: &chatapi.MessageRefRequest{ID: elsewhere.ID, CreatedAt: elsewhere.CreatedAt},
	})
	if !errors.Is(err, domain.ErrReplyOutsideThread) {
		t.Fatalf("err = %v, want ErrReplyOutsideThread", err)
	}
	if got := status(t, err); got != 422 {
		t.Errorf("status = %d, want 422", got)
	}
}

// Unsending is for taking words back, so a quote of a redacted message must not keep showing
// them. Read live is what makes that fall out rather than needing a sweep over every reply.
func TestSendMessage_QuoteOfARedactedMessageSaysSo(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	regretted, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "số điện thoại của tôi là …",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: bob, ConversationID: thread.ID, Body: "ok",
		ReplyTo: &chatapi.MessageRefRequest{ID: regretted.ID, CreatedAt: regretted.CreatedAt},
	}); err != nil {
		t.Fatalf("SendMessage reply: %v", err)
	}

	if err := h.svc.RedactMessage(ctx, chatapi.RedactMessageRequest{
		ActorID: alice, ID: regretted.ID, CreatedAt: regretted.CreatedAt,
	}); err != nil {
		t.Fatalf("RedactMessage: %v", err)
	}

	page, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: bob, ID: thread.ID, Limit: 20})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var checked bool
	for _, m := range page.Data {
		if m.ReplyTo == nil {
			continue
		}
		checked = true
		if !m.ReplyTo.Redacted {
			t.Error("reply_to.redacted = false, want true after the quoted message was unsent")
		}
		if m.ReplyTo.Preview != "" {
			t.Errorf("reply_to.preview = %q, want it gone with the message", m.ReplyTo.Preview)
		}
	}
	if !checked {
		t.Fatal("no reply on the page to check")
	}
}

// A quote is a projection of a message like the row is, so the desk's anonymity has to hold
// through it: the requester learning which moderator wrote what defeats the whole masking.
func TestListMessages_QuoteOfASupportReplyStaysAnonymous(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	thread, err := h.svc.OpenTicketThread(ctx, chatapi.OpenTicketThreadRequest{
		RequesterID: alice, TicketID: ticketID, Body: "đơn của tôi bị giao sai",
	})
	if err != nil {
		t.Fatalf("OpenTicketThread: %v", err)
	}
	staffSaid, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: moderator, ConversationID: thread.ID, Body: "bên mình đang kiểm tra",
	})
	if err != nil {
		t.Fatalf("SendMessage as staff: %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: "bao lâu ạ?",
		ReplyTo: &chatapi.MessageRefRequest{ID: staffSaid.ID, CreatedAt: staffSaid.CreatedAt},
	}); err != nil {
		t.Fatalf("SendMessage reply: %v", err)
	}

	page, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: alice, ID: thread.ID, Limit: 20})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var checked bool
	for _, m := range page.Data {
		if m.ReplyTo == nil {
			continue
		}
		checked = true
		if m.ReplyTo.SenderID != nil {
			t.Errorf("reply_to.sender_id = %v, want null for the desk", m.ReplyTo.SenderID)
		}
		if !m.ReplyTo.FromSupport {
			t.Error("reply_to.from_support = false, want true")
		}
	}
	if !checked {
		t.Fatal("no reply on the page to check")
	}

	// Staff read the same thread and see the colleague, which is what makes it reviewable.
	staffPage, err := h.svc.ListMessages(ctx, chatapi.ListMessagesRequest{ActorID: moderator, ID: thread.ID, Limit: 20})
	if err != nil {
		t.Fatalf("ListMessages as staff: %v", err)
	}
	checked = false
	for _, m := range staffPage.Data {
		if m.ReplyTo == nil {
			continue
		}
		checked = true
		if m.ReplyTo.SenderID == nil || *m.ReplyTo.SenderID != moderator {
			t.Errorf("reply_to.sender_id for staff = %v, want %v", m.ReplyTo.SenderID, moderator)
		}
	}
	if !checked {
		t.Fatal("no reply on the staff page to check")
	}
}

// A quote is cut by runes, not bytes: a Vietnamese sentence is mostly multi-byte, and a byte
// cut produces a replacement character instead of a shorter sentence.
func TestSendMessage_LongQuoteIsCutOnARuneBoundary(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	long := strings.Repeat("để", 400)
	original, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID, Body: long,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	reply, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: bob, ConversationID: thread.ID, Body: "ok",
		ReplyTo: &chatapi.MessageRefRequest{ID: original.ID, CreatedAt: original.CreatedAt},
	})
	if err != nil {
		t.Fatalf("SendMessage reply: %v", err)
	}
	if reply.ReplyTo == nil {
		t.Fatal("reply_to = nil")
	}
	if strings.ContainsRune(reply.ReplyTo.Preview, '�') {
		t.Error("preview holds a replacement character, so it was cut mid-rune")
	}
	if got := len([]rune(reply.ReplyTo.Preview)); got > 121 {
		t.Errorf("preview = %d runes, want the cap plus the ellipsis", got)
	}
}

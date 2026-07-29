package chat_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/module/chat"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	saved  *domain.Message
	listed []domain.Message
}

func (f *fakeRepo) Save(_ context.Context, m *domain.Message) error {
	m.ID = 1
	f.saved = m
	return nil
}

func (f *fakeRepo) ListByConversation(_ context.Context, conversationID int64, limit, offset int) ([]domain.Message, error) {
	return f.listed, nil
}

func TestSendMessage(t *testing.T) {
	repo := &fakeRepo{}
	svc := chat.NewService(repo, slog.Default())
	got, err := svc.SendMessage(context.Background(), chatapi.SendMessageRequest{ConversationID: id.Of[id.Conversation](3), SenderID: id.Of[id.Account](7), Body: "Hello"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got.ID != id.Of[id.Message](1) || got.Body != "Hello" {
		t.Fatalf("unexpected message: %+v", got)
	}
}

func TestListMessages_ReturnsMapped(t *testing.T) {
	repo := &fakeRepo{listed: []domain.Message{
		{ID: 1, ConversationID: 3, SenderID: 7, Body: "Hello"},
		{ID: 2, ConversationID: 3, SenderID: 8, Body: "Hi"},
	}}
	svc := chat.NewService(repo, slog.Default())

	got, err := svc.ListMessages(context.Background(), chatapi.ListMessagesRequest{ConversationID: id.Of[id.Conversation](3)})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Body != "Hello" {
		t.Fatalf("expected Hello, got %+v", got[0])
	}
}

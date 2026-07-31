// Package chattest provides a stub chatapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call
// shows up as an obviously wrong status rather than as a plausible zero value.
package chattest

import (
	"context"

	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/shared/errx"
)

// Stub implements chatapi.Service by refusing everything.
type Stub struct{}

var _ chatapi.Service = Stub{}

func (Stub) ListConversations(context.Context, chatapi.ListConversationsRequest) (chatapi.ConversationPage, error) {
	return chatapi.ConversationPage{}, errx.ErrNotImplemented
}

func (Stub) StartConversation(context.Context, chatapi.StartConversationRequest) (chatapi.Conversation, error) {
	return chatapi.Conversation{}, errx.ErrNotImplemented
}

func (Stub) GetConversation(context.Context, chatapi.GetConversationRequest) (chatapi.Conversation, error) {
	return chatapi.Conversation{}, errx.ErrNotImplemented
}

func (Stub) GetUnreadCount(context.Context, chatapi.UnreadCountRequest) (chatapi.UnreadCount, error) {
	return chatapi.UnreadCount{}, errx.ErrNotImplemented
}

func (Stub) ListMessages(context.Context, chatapi.ListMessagesRequest) (chatapi.MessagePage, error) {
	return chatapi.MessagePage{}, errx.ErrNotImplemented
}

func (Stub) SendMessage(context.Context, chatapi.SendMessageRequest) (chatapi.Message, error) {
	return chatapi.Message{}, errx.ErrNotImplemented
}

func (Stub) MarkConversationRead(context.Context, chatapi.MarkConversationReadRequest) (chatapi.Conversation, error) {
	return chatapi.Conversation{}, errx.ErrNotImplemented
}

func (Stub) UpdateMessage(context.Context, chatapi.UpdateMessageRequest) (chatapi.Message, error) {
	return chatapi.Message{}, errx.ErrNotImplemented
}

func (Stub) RedactMessage(context.Context, chatapi.RedactMessageRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) PostSystemMessage(context.Context, chatapi.PostSystemMessageRequest) (chatapi.Message, error) {
	return chatapi.Message{}, errx.ErrNotImplemented
}

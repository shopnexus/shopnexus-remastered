// Package chatapi is the published contract of the chat service.
package chatapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type SendMessageRequest struct {
	ConversationID id.ID[id.Conversation] `json:"conversation_id" validate:"required"`
	SenderID       id.ID[id.Account]      `json:"-" validate:"required"`
	Body           string                 `json:"body" validate:"required,max=4000"`
}

type ListMessagesRequest struct {
	ConversationID id.ID[id.Conversation] `validate:"required"`
	Limit          int                    `validate:"gte=0,lte=200"`
	Offset         int                    `validate:"gte=0"`
}

type Message struct {
	ID             id.ID[id.Message]      `json:"id"`
	ConversationID id.ID[id.Conversation] `json:"conversation_id"`
	SenderID       id.ID[id.Account]      `json:"sender_id"`
	Body           string                 `json:"body"`
}

type Service interface {
	SendMessage(ctx context.Context, req SendMessageRequest) (Message, error)
	ListMessages(ctx context.Context, req ListMessagesRequest) ([]Message, error)
}

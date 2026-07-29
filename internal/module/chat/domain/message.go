// Package domain: chat entity + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type Message struct {
	ID             int64
	ConversationID int64  `validate:"required"`
	SenderID       int64  `validate:"required"`
	Body           string `validate:"required"`
	CreatedAt      time.Time
}

func NewMessage(conversationID, senderID int64, body string) (Message, error) {
	m := Message{ConversationID: conversationID, SenderID: senderID, Body: body}
	if err := validation.Default().Struct(m); err != nil {
		return Message{}, errx.ErrValidation.Fmt(err.Error())
	}
	return m, nil
}

// Package port: interface the chat adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/chat/domain"
)

type Repository interface {
	Save(ctx context.Context, m *domain.Message) error
	ListByConversation(ctx context.Context, conversationID int64, limit, offset int) ([]domain.Message, error)
}

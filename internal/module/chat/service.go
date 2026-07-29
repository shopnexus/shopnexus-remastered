// Package chat implements chatapi.Service.
package chat

import (
	"context"
	"fmt"
	"log/slog"

	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/shared/id"
)

type Service struct {
	repo port.Repository
	log  *slog.Logger
}

func NewService(repo port.Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

var _ chatapi.Service = (*Service)(nil)

func (s *Service) SendMessage(ctx context.Context, req chatapi.SendMessageRequest) (chatapi.Message, error) {
	m, err := domain.NewMessage(req.ConversationID.Int64(), req.SenderID.Int64(), req.Body)
	if err != nil {
		return chatapi.Message{}, err
	}
	if err := s.repo.Save(ctx, &m); err != nil {
		return chatapi.Message{}, fmt.Errorf("save message: %w", err)
	}
	return s.toAPIMessage(m), nil
}

func (s *Service) ListMessages(ctx context.Context, req chatapi.ListMessagesRequest) ([]chatapi.Message, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := s.repo.ListByConversation(ctx, req.ConversationID.Int64(), limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	out := make([]chatapi.Message, 0, len(rows))
	for _, m := range rows {
		out = append(out, s.toAPIMessage(m))
	}
	return out, nil
}

func (s *Service) toAPIMessage(m domain.Message) chatapi.Message {
	return chatapi.Message{
		ID:             id.Of[id.Message](m.ID),
		ConversationID: id.Of[id.Conversation](m.ConversationID),
		SenderID:       id.Of[id.Account](m.SenderID),
		Body:           m.Body,
	}
}

package chatbiz

import (
	"context"
	"encoding/json"
	"fmt"

	accountmodel "shopnexus-server/internal/module/account/model"
	chatdb "shopnexus-server/internal/module/chat/db/sqlc"
	chatmodel "shopnexus-server/internal/module/chat/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type CreateConversationParams struct {
	Account  accountmodel.AuthenticatedAccount
	SellerID uuid.UUID `validate:"required"`
}

// CreateConversation creates a new conversation between a customer and vendor, or returns the existing one.
func (b *ChatHandler) CreateConversation(
	ctx context.Context,
	params CreateConversationParams,
) (chatdb.ChatConversation, error) {
	var zero chatdb.ChatConversation
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create conversation params: %w", err)
	}

	existing, err := b.storage.Querier().GetConversationByParticipants(ctx, chatdb.GetConversationByParticipantsParams{
		BuyerID:  params.Account.ID,
		SellerID: params.SellerID,
	})

	if err == nil {
		return existing, nil
	}

	result, err := b.storage.Querier().CreateDefaultConversation(ctx, chatdb.CreateDefaultConversationParams{
		BuyerID:  params.Account.ID,
		SellerID: params.SellerID,
	})
	if err != nil {
		return zero, fmt.Errorf("db create default conversation: %w", err)
	}

	return result, nil
}

// GetConversation returns a conversation by its ID.
func (b *ChatHandler) GetConversation(ctx context.Context, id uuid.UUID) (chatdb.ChatConversation, error) {
	conv, err := b.storage.Querier().GetConversationByID(ctx, id)
	if err != nil {
		return chatdb.ChatConversation{}, fmt.Errorf("db get conversation by id: %w", err)
	}
	return conv, nil
}

type ListConversationParams struct {
	paginate.Params

	Account accountmodel.AuthenticatedAccount
}

// ListConversation returns a paginated list of conversations for the authenticated account.
func (b *ChatHandler) ListConversation(
	ctx context.Context,
	params ListConversationParams,
) (paginate.PaginateResult[chatdb.ChatConversation], error) {
	var zero paginate.PaginateResult[chatdb.ChatConversation]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list conversation params: %w", err)
	}

	conversations, err := b.storage.Querier().ListConversationByAccount(ctx, chatdb.ListConversationByAccountParams{
		AccountID: params.Account.ID,
		Limit:     params.Limit.Int32,
		Offset:    params.Offset().Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("db list conversation by account: %w", err)
	}

	total, err := b.storage.Querier().CountConversationByAccount(ctx, params.Account.ID)
	if err != nil {
		return zero, fmt.Errorf("db count conversation by account: %w", err)
	}

	return paginate.PaginateResult[chatdb.ChatConversation]{
		PageParams: params.Params,
		Data:       conversations,
		Total:      null.IntFrom(total),
	}, nil
}

type SendMessageParams struct {
	Account        accountmodel.AuthenticatedAccount
	ConversationID uuid.UUID              `validate:"required"`
	Type           chatdb.ChatMessageType `validate:"required,validateFn=Valid"`
	Content        string                 `validate:"required"`
	Metadata       json.RawMessage
}

// SendMessage sends a message in a conversation the account participates in.
func (b *ChatHandler) SendMessage(ctx context.Context, params SendMessageParams) (chatdb.ChatMessage, error) {
	var zero chatdb.ChatMessage
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate send message params: %w", err)
	}

	conv, err := b.storage.Querier().GetConversationByID(ctx, params.ConversationID)
	if err != nil {
		return zero, chatmodel.ErrConversationNotFound
	}

	if conv.BuyerID != params.Account.ID && conv.SellerID != params.Account.ID {
		return zero, chatmodel.ErrNotParticipant
	}

	msg, err := b.storage.Querier().CreateChatMessage(ctx, chatdb.CreateChatMessageParams{
		ConversationID: params.ConversationID,
		SenderID:       params.Account.ID,
		Type:           params.Type,
		Content:        params.Content,
		Data:           params.Metadata,
	})
	if err != nil {
		return zero, fmt.Errorf("db create chat message: %w", err)
	}

	if err := b.storage.Querier().UpdateConversationLastMessage(ctx, params.ConversationID); err != nil {
		return zero, fmt.Errorf("db update conversation last message: %w", err)
	}

	// Push new_message to both participants via SSE
	recipientID := conv.BuyerID
	if recipientID == params.Account.ID {
		recipientID = conv.SellerID
	}
	for _, id := range []uuid.UUID{params.Account.ID, recipientID} {
		if err = b.common.Guaranteed().Send().PushEvent(ctx, commonbiz.PushEventParams{
			AccountID: id,
			Type:      commonbiz.SSENewMessage,
			Data:      msg,
		}); err != nil {
			return zero, fmt.Errorf("push new message event: %w", err)
		}
	}

	return msg, nil
}

type ListMessageParams struct {
	paginate.Params

	Account        accountmodel.AuthenticatedAccount
	ConversationID uuid.UUID `validate:"required"`
}

// ListMessage returns a paginated list of messages in a conversation.
func (b *ChatHandler) ListMessage(
	ctx context.Context,
	params ListMessageParams,
) (paginate.PaginateResult[chatdb.ChatMessage], error) {
	var zero paginate.PaginateResult[chatdb.ChatMessage]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list message params: %w", err)
	}

	conv, err := b.storage.Querier().GetConversationByID(ctx, params.ConversationID)
	if err != nil {
		return zero, chatmodel.ErrConversationNotFound
	}

	if conv.BuyerID != params.Account.ID && conv.SellerID != params.Account.ID {
		return zero, chatmodel.ErrNotParticipant
	}

	messages, err := b.storage.Querier().ListMessageByConversation(ctx, chatdb.ListMessageByConversationParams{
		ConversationID: params.ConversationID,
		Limit:          params.Limit.Int32,
		Offset:         params.Offset().Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("db list message by conversation: %w", err)
	}

	total, err := b.storage.Querier().CountMessageByConversation(ctx, params.ConversationID)
	if err != nil {
		return zero, fmt.Errorf("db count message by conversation: %w", err)
	}

	return paginate.PaginateResult[chatdb.ChatMessage]{
		PageParams: params.Params,
		Data:       messages,
		Total:      null.IntFrom(total),
	}, nil
}

type MarkReadParams struct {
	Account        accountmodel.AuthenticatedAccount
	ConversationID uuid.UUID `validate:"required"`
}

// MarkRead marks all messages in a conversation as read for the authenticated account.
func (b *ChatHandler) MarkRead(ctx context.Context, params MarkReadParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate mark read params: %w", err)
	}
	if err := b.storage.Querier().MarkMessagesRead(ctx, chatdb.MarkMessagesReadParams{
		ConversationID: params.ConversationID,
		ReaderID:       params.Account.ID,
	}); err != nil {
		return fmt.Errorf("db mark messages read: %w", err)
	}

	// Push read_receipt to the other participant via SSE
	conv, err := b.storage.Querier().GetConversationByID(ctx, params.ConversationID)
	if err == nil {
		recipientID := conv.BuyerID
		if recipientID == params.Account.ID {
			recipientID = conv.SellerID
		}
		if err = b.common.Guaranteed().Send().PushEvent(ctx, commonbiz.PushEventParams{
			AccountID: recipientID,
			Type:      commonbiz.SSEReadReceipt,
			Data: map[string]any{
				"conversation_id": params.ConversationID,
				"reader_id":       params.Account.ID,
			},
		}); err != nil {
			return fmt.Errorf("push read receipt event: %w", err)
		}
	}

	return nil
}

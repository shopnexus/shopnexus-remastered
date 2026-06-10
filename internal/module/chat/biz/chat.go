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
	restate "github.com/restatedev/sdk-go"
)

type CreateConversationParams struct {
	Account  accountmodel.AuthenticatedAccount
	SellerID uuid.UUID `validate:"required"`
}

// CreateConversation creates a new conversation between a customer and vendor, or returns the existing one.
func (b *ChatHandler) CreateConversation(
	ctx restate.Context,
	params CreateConversationParams,
) (chatdb.ChatConversation, error) {
	var zero chatdb.ChatConversation
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create conversation params: %w", err)
	}

	// decision: return the existing conversation if the pair already chats.
	existing, err := restate.Run(ctx, func(rctx restate.RunContext) (chatdb.ChatConversation, error) {
		conv, err := b.storage.Querier().GetConversationByParticipants(rctx, chatdb.GetConversationByParticipantsParams{
			BuyerID:  params.Account.ID,
			SellerID: params.SellerID,
		})
		if err != nil {
			return zero, nil // not found → empty conversation signals create
		}
		return conv, nil
	})
	if err != nil {
		return zero, err
	}
	if existing.ID != uuid.Nil {
		return existing, nil
	}

	// execution: create the default conversation.
	result, err := restate.Run(ctx, func(rctx restate.RunContext) (chatdb.ChatConversation, error) {
		conv, err := b.storage.Querier().CreateDefaultConversation(rctx, chatdb.CreateDefaultConversationParams{
			BuyerID:  params.Account.ID,
			SellerID: params.SellerID,
		})
		if err != nil {
			return zero, fmt.Errorf("db create default conversation: %w", err)
		}
		return conv, nil
	})
	if err != nil {
		return zero, err
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
func (b *ChatHandler) SendMessage(ctx restate.Context, params SendMessageParams) (chatdb.ChatMessage, error) {
	var zero chatdb.ChatMessage
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate send message params: %w", err)
	}

	// decision: load the conversation and check the account participates.
	conv, err := restate.Run(ctx, func(rctx restate.RunContext) (chatdb.ChatConversation, error) {
		conv, err := b.storage.Querier().GetConversationByID(rctx, params.ConversationID)
		if err != nil {
			return chatdb.ChatConversation{}, chatmodel.ErrConversationNotFound
		}
		if conv.BuyerID != params.Account.ID && conv.SellerID != params.Account.ID {
			return chatdb.ChatConversation{}, chatmodel.ErrNotParticipant
		}
		return conv, nil
	})
	if err != nil {
		return zero, err
	}

	// execution: persist the message and bump the conversation's last message.
	msg, err := restate.Run(ctx, func(rctx restate.RunContext) (chatdb.ChatMessage, error) {
		msg, err := b.storage.Querier().CreateChatMessage(rctx, chatdb.CreateChatMessageParams{
			ConversationID: params.ConversationID,
			SenderID:       params.Account.ID,
			Type:           params.Type,
			Content:        params.Content,
			Data:           params.Metadata,
		})
		if err != nil {
			return zero, fmt.Errorf("db create chat message: %w", err)
		}
		if err := b.storage.Querier().UpdateConversationLastMessage(rctx, params.ConversationID); err != nil {
			return zero, fmt.Errorf("db update conversation last message: %w", err)
		}
		return msg, nil
	})
	if err != nil {
		return zero, err
	}

	// tail: push new_message to both participants via SSE.
	recipientID := conv.BuyerID
	if recipientID == params.Account.ID {
		recipientID = conv.SellerID
	}
	err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		for _, id := range []uuid.UUID{params.Account.ID, recipientID} {
			if err := b.common.Guaranteed().Send().PushEvent(rctx, commonbiz.PushEventParams{
				AccountID: id,
				Type:      commonbiz.SSENewMessage,
				Data:      msg,
			}); err != nil {
				return fmt.Errorf("push new message event: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return zero, err
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
func (b *ChatHandler) MarkRead(ctx restate.Context, params MarkReadParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate mark read params: %w", err)
	}

	// execution: mark all messages read for this reader.
	if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if err := b.storage.Querier().MarkMessagesRead(rctx, chatdb.MarkMessagesReadParams{
			ConversationID: params.ConversationID,
			ReaderID:       params.Account.ID,
		}); err != nil {
			return fmt.Errorf("db mark messages read: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	// tail: push read_receipt to the other participant via SSE (best-effort on conv load).
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		conv, err := b.storage.Querier().GetConversationByID(rctx, params.ConversationID)
		if err != nil {
			return nil
		}
		recipientID := conv.BuyerID
		if recipientID == params.Account.ID {
			recipientID = conv.SellerID
		}
		if err := b.common.Guaranteed().Send().PushEvent(rctx, commonbiz.PushEventParams{
			AccountID: recipientID,
			Type:      commonbiz.SSEReadReceipt,
			Data: map[string]any{
				"conversation_id": params.ConversationID,
				"reader_id":       params.Account.ID,
			},
		}); err != nil {
			return fmt.Errorf("push read receipt event: %w", err)
		}
		return nil
	})
}

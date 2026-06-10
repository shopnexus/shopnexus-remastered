package chatbiz

import (
	"context"

	chatdb "shopnexus-server/internal/module/chat/db/sqlc"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// ChatBiz is the client interface for ChatHandler, which is used by other modules to call ChatHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface ChatBiz -service Chat
type ChatBiz interface {
	// Conversation
	CreateConversation(ctx restate.Context, params CreateConversationParams) (chatdb.ChatConversation, error)
	GetConversation(ctx context.Context, id uuid.UUID) (chatdb.ChatConversation, error)
	ListConversation(
		ctx context.Context,
		params ListConversationParams,
	) (paginate.PaginateResult[chatdb.ChatConversation], error)

	// Message
	SendMessage(ctx restate.Context, params SendMessageParams) (chatdb.ChatMessage, error)
	ListMessage(ctx context.Context, params ListMessageParams) (paginate.PaginateResult[chatdb.ChatMessage], error)
	MarkRead(ctx restate.Context, params MarkReadParams) error
}

type ChatStorage = pgsqlc.Storage[*chatdb.Queries]

// ChatHandler implements the core business logic for the chat module.
type ChatHandler struct {
	storage ChatStorage
	common  commonbiz.CommonBizClient
}

func (b *ChatHandler) ServiceName() string {
	return "Chat"
}

// NewChatHandler creates a new ChatHandler with the given dependencies.
func NewChatHandler(storage ChatStorage, common commonbiz.CommonBizClient) *ChatHandler {
	return &ChatHandler{storage: storage, common: common}
}

package chat

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	chatbiz "shopnexus-server/internal/module/chat/biz"
	chatconfig "shopnexus-server/internal/module/chat/config"
	chatdb "shopnexus-server/internal/module/chat/db/sqlc"
	chatecho "shopnexus-server/internal/module/chat/transport/echo"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the chat module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("chat",
	fxinfra.Providers[*chatconfig.Config]("chat"),
	fx.Provide(
		chatconfig.NewConfig,
		NewChatStorage,
		NewChatHandler,
		NewChatBiz,
		chatecho.NewHandler,
	),
	fx.Provide(
		fx.Annotate(
			func(b *chatbiz.ChatHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(chatbiz.NewChatService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *chatbiz.ChatHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { chatbiz.RegisterChatBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
	),
	fx.Invoke(
		chatecho.NewHandler,
	),
)

func NewChatHandler(storage chatbiz.ChatStorage, common commonbiz.CommonBizClient) *chatbiz.ChatHandler {
	return chatbiz.NewChatHandler(storage, common)
}

// NewChatStorage creates a new chat storage backed by PostgreSQL.
func NewChatStorage(pool pgsqlc.TxBeginner) chatbiz.ChatStorage {
	return pgsqlc.NewStorage(pool, chatdb.New(pool))
}

// NewChatBiz creates the chat client. BestEffort calls run in-process.
func NewChatBiz(cfg *chatconfig.Config, biz *chatbiz.ChatHandler) chatbiz.ChatBizClient {
	return chatbiz.NewChatBizClientInProcess(cfg.Restate.IngressAddress, biz)
}

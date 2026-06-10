package catalog

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/fxinfra"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogconfig "shopnexus-server/internal/module/catalog/config"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogecho "shopnexus-server/internal/module/catalog/transport/echo"
	catalogworkers "shopnexus-server/internal/module/catalog/workers"
	"shopnexus-server/internal/provider/llm"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the catalog module dependencies. Catalog OWNS llm
// (it is the only module that uses it). Pool/Cache/Logger are fx.Private —
// each is constructed from THIS module's own Postgres/Redis/Log config and
// invisible to other modules.
var Module = fx.Module("catalog",
	fxinfra.Providers[*catalogconfig.Config]("catalog"),
	fx.Provide(
		catalogconfig.NewConfig,
		NewLLMClient,
		NewCatalogStorage,
		catalogbiz.NewCatalogHandler,
		NewCatalogBiz,
		catalogecho.NewHandler,
	),
	fx.Invoke(
		catalogecho.NewHandler,
		catalogworkers.Register,
	),
)

// NewLLMClient builds the configured provider and wraps it in a CachingClient
// so repeated embedding queries (the common search case) skip the network hop.
func NewLLMClient(cfg *catalogconfig.Config, cache cache.Client) (llm.Client, error) {
	var (
		client llm.Client
		err    error
	)

	switch cfg.LLM.Provider {
	case "python":
		client = llm.NewPythonClient(llm.PythonConfig{
			URL: cfg.LLM.Python.URL,
		})
	case "openai":
		client = llm.NewOpenAIClient(llm.OpenAIConfig{
			APIKey:     cfg.LLM.OpenAI.APIKey,
			BaseURL:    cfg.LLM.OpenAI.BaseURL,
			EmbedModel: cfg.LLM.OpenAI.EmbedModel,
			ChatModel:  cfg.LLM.OpenAI.ChatModel,
		})
	case "bedrock":
		client, err = llm.NewBedrockClient(context.Background(), llm.BedrockConfig{
			Region:       cfg.LLM.Bedrock.Region,
			EmbedModelID: cfg.LLM.Bedrock.EmbedModelID,
			ChatModelID:  cfg.LLM.Bedrock.ChatModelID,
		})
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", cfg.LLM.Provider)
	}
	if err != nil {
		return nil, err
	}

	return llm.NewCachingClient(client, cache, cfg.LLM.EmbedCacheTTL), nil
}

// NewCatalogStorage creates a new catalog storage backed by PostgreSQL.
func NewCatalogStorage(pool pgsqlc.TxBeginner) catalogbiz.CatalogStorage {
	return pgsqlc.NewStorage(pool, catalogdb.New(pool))
}

// NewCatalogBiz creates a Restate-backed client for the catalog module.
func NewCatalogBiz(cfg *catalogconfig.Config) catalogbiz.CatalogBizClient {
	return catalogbiz.NewCatalogBizClientRemote(cfg.Restate.IngressAddress, cfg.Restate.IngressAddress)
}

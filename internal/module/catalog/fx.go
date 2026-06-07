package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/milvus"
	"shopnexus-server/internal/infras/pg"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogconfig "shopnexus-server/internal/module/catalog/config"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogecho "shopnexus-server/internal/module/catalog/transport/echo"
	"shopnexus-server/internal/provider/llm"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the catalog module dependencies. Catalog OWNS milvus + llm
// (it is the only module that uses them). Pool/Cache/Logger are fx.Private —
// each is constructed from THIS module's own Postgres/Redis/Log config and
// invisible to other modules.
var Module = fx.Module("catalog",
	fx.Provide(
		NewPool,
		NewCache,
		NewLogger,
		fx.Private,
	),
	fx.Provide(
		catalogconfig.NewConfig,
		NewMilvusClient,
		NewLLMClient,
		NewCatalogStorage,
		catalogbiz.NewCatalogHandler,
		NewCatalogBiz,
		catalogecho.NewHandler,
	),
	fx.Invoke(
		catalogecho.NewHandler,
	),
)

func NewPool(cfg *catalogconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
	pool, err := pg.New(pg.Options{
		Url:             cfg.Postgres.Url,
		Host:            cfg.Postgres.Host,
		Port:            cfg.Postgres.Port,
		Username:        cfg.Postgres.Username,
		Password:        cfg.Postgres.Password,
		Database:        cfg.Postgres.Database,
		MaxConnections:  cfg.Postgres.MaxConnections,
		MaxConnIdleTime: cfg.Postgres.MaxConnIdleTime,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return pool.Ping(ctx) },
		OnStop:  func(context.Context) error { pool.Close(); return nil },
	})
	return pool, nil
}

func NewCache(cfg *catalogconfig.Config) (cache.Client, error) {
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{fmt.Sprintf("%s:%s", cfg.Redis.Host, cfg.Redis.Port)},
		Password:    cfg.Redis.Password,
	})
	if err != nil {
		return nil, err
	}
	return cache.NewRedisStructClient(rdb, cache.Config{
		Encoder: json.Marshal,
		Decoder: json.Unmarshal,
	})
}

func NewLogger(cfg *catalogconfig.Config) *slog.Logger {
	return buildLogger(cfg.Log.Level, cfg.Log.AddSource, "catalog")
}

// buildLogger is the shared module-logger constructor — copied across module
// fx.go files to keep each module fully self-describing (no shared helper).
func buildLogger(levelStr string, addSource bool, module string) *slog.Logger {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: addSource,
	})
	return slog.New(h).With(slog.String("module", module))
}

func NewMilvusClient(cfg *catalogconfig.Config, lc fx.Lifecycle) (*milvus.Client, error) {
	client, err := milvus.NewClient(context.Background(), milvus.Config{
		Address: cfg.Milvus.Address,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { return client.Close(ctx) },
	})
	return client, nil
}

func NewLLMClient(cfg *catalogconfig.Config) (llm.Client, error) {
	switch cfg.LLM.Provider {
	case "python":
		return llm.NewPythonClient(llm.PythonConfig{
			URL: cfg.LLM.Python.URL,
		}), nil
	case "openai":
		return llm.NewOpenAIClient(llm.OpenAIConfig{
			APIKey:     cfg.LLM.OpenAI.APIKey,
			BaseURL:    cfg.LLM.OpenAI.BaseURL,
			EmbedModel: cfg.LLM.OpenAI.EmbedModel,
			ChatModel:  cfg.LLM.OpenAI.ChatModel,
		}), nil
	case "bedrock":
		return llm.NewBedrockClient(context.Background(), llm.BedrockConfig{
			Region:       cfg.LLM.Bedrock.Region,
			EmbedModelID: cfg.LLM.Bedrock.EmbedModelID,
			ChatModelID:  cfg.LLM.Bedrock.ChatModelID,
		})
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", cfg.LLM.Provider)
	}
}

// NewCatalogStorage creates a new catalog storage backed by PostgreSQL.
func NewCatalogStorage(pool pgsqlc.TxBeginner) catalogbiz.CatalogStorage {
	return pgsqlc.NewStorage(pool, catalogdb.New(pool))
}

// NewCatalogBiz creates a Restate-backed client for the catalog module.
func NewCatalogBiz(cfg *catalogconfig.Config) catalogbiz.CatalogBiz {
	return catalogbiz.NewCatalogRestateClient(cfg.Restate.IngressAddress)
}

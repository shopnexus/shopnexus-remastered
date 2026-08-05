// Command embedder keeps catalog's vectors in step with its text.
//
// Three tables carry an `embedding_stale_at`: listing, category and tag. A write that changes
// what a row *says* sets the mark, and this worker is the only thing that clears it. The mark
// is therefore the queue — a work list that is a property of the data rather than a message
// somebody has to not lose, so a row edited during a deploy is still stale afterwards and a
// pass that already ran finds nothing to do.
//
// Its own binary rather than a sweep inside the gateway: a pass is a batch of transformer
// inferences, and the process serving requests should not be the one holding that. It scales
// and restarts on its own, and a deployment that wants no semantic search simply does not run
// it — search falls back to trigram, which is what an unembedded row already gets.
//
//	go run ./cmd/embedder          # drain on EMBEDDING_INTERVAL until stopped
//	go run ./cmd/embedder -once    # drain once and exit; the backfill, and what CI runs
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/catalog"
	catalogpg "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/provider/embedding"
	"shopnexus/internal/provider/embedding/bgem3"
	embeddingmock "shopnexus/internal/provider/embedding/mock"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/logger"
	"shopnexus/internal/shared/validation"
)

// once makes the worker a one-shot: drain every queue, then exit — non-zero if anything
// failed. That is the backfill after an import and the shape a CI step or a Kubernetes Job
// wants, and it is the same code the daemon runs on its ticker.
var once = flag.Bool("once", false, "drain the queues once and exit instead of running on the interval")

func main() {
	flag.Parse()
	fx.New(appOptions(*once)).Run()
}

func appOptions(once bool) fx.Option {
	return fx.Options(
		fx.Provide(
			validation.Default,
			loadConfig,
			newLogger,
			newPool,
			fx.Annotate(newEmbeddings, fx.As(new(port.Embeddings))),
			newEmbeddingClient,
			newIndexer,
		),
		fx.Supply(runMode{once: once}),
		fx.Invoke(run),
		fx.WithLogger(func(log *slog.Logger) fxevent.Logger {
			return &fxevent.SlogLogger{Logger: log}
		}),
	)
}

type runMode struct{ once bool }

func loadConfig(v *validator.Validate) (*config.Config, error) { return config.Load(v) }

func newLogger(cfg *config.Config) *slog.Logger {
	return logger.New(logger.Options{Level: cfg.LogLevel, Service: "embedder"})
}

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.CatalogDBDSN, "catalog")
	if err != nil {
		return nil, fmt.Errorf("open catalog db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newEmbeddings(pool *pgxpool.Pool) *catalogpg.Embeddings { return catalogpg.NewEmbeddings(pool) }

// newEmbeddingClient picks the model. `mock` hashes the words, which is enough to exercise the
// queue, both vector columns and their indexes without the real model — and useless for
// retrieval quality, which is the trade a laptop makes.
func newEmbeddingClient(cfg *config.Config) (embedding.Client, error) {
	if cfg.EmbeddingProvider != bgem3.Name {
		return embeddingmock.New(cfg.EmbeddingDimensions), nil
	}
	return bgem3.New(bgem3.Config{
		BaseURL:    cfg.EmbeddingBaseURL,
		APIKey:     cfg.EmbeddingAPIKey,
		Dimensions: cfg.EmbeddingDimensions,
		Timeout:    cfg.EmbeddingTimeout,
		// Instrumented transport, no http.Client.Timeout: the per-call budget is on the
		// context, where a slow model is bounded without truncating a response mid-read.
		HTTPClient: &http.Client{
			Transport: httpx.ObserveOutbound(bgem3.Name, http.DefaultTransport, nil),
		},
	})
}

func newIndexer(repo port.Embeddings, client embedding.Client, cfg *config.Config, log *slog.Logger) (*catalog.Indexer, error) {
	return catalog.NewIndexer(repo, client, catalog.IndexerConfig{
		Batch:        cfg.EmbeddingBatchSize,
		MaxTextChars: cfg.EmbeddingMaxTextChars,
	}, log)
}

// run starts the worker inside fx's lifecycle so the pool is opened before it and closed
// after it, and so a SIGINT cancels the pass in flight rather than killing it mid-transaction.
func run(lc fx.Lifecycle, sd fx.Shutdowner, mode runMode, idx *catalog.Indexer, cfg *config.Config,
	client embedding.Client, log *slog.Logger) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("embedder starting", "model", client.Name(), "dimensions", cfg.EmbeddingDimensions,
				"batch", cfg.EmbeddingBatchSize, "once", mode.once)
			go func() {
				defer close(done)
				if !mode.once {
					idx.Run(ctx, cfg.EmbeddingInterval)
					return
				}
				n, err := idx.Pass(ctx)
				log.Info("embedded", "rows", n)
				// A one-shot has to exit non-zero on failure, or a backfill that embedded
				// nothing looks the same as one that had nothing to do.
				code := 0
				if err != nil {
					code = 1
				}
				if err := sd.Shutdown(fx.ExitCode(code)); err != nil {
					log.Error("shutdown", "err", err)
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			<-done
			return nil
		},
	})
}

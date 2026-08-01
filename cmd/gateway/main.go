package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/redis/rueidis"
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"shopnexus/internal/config"
	"shopnexus/internal/gateway"
	"shopnexus/internal/infra/cache"
	"shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/account"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/chat"
	"shopnexus/internal/module/finance"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/module/order"
	"shopnexus/internal/module/trust"
	"shopnexus/internal/provider"
	"shopnexus/internal/provider/kyc"
	"shopnexus/internal/provider/kyc/fptai"
	kycmock "shopnexus/internal/provider/kyc/mock"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/provider/notify/esms"
	notifymock "shopnexus/internal/provider/notify/mock"
	smtpnotify "shopnexus/internal/provider/notify/smtp"
	"shopnexus/internal/provider/oauth"
	oauthmock "shopnexus/internal/provider/oauth/mock"
	oidcverify "shopnexus/internal/provider/oauth/oidc"
	"shopnexus/internal/provider/payment"
	paymentmock "shopnexus/internal/provider/payment/mock"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/logger"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

func main() {
	fx.New(
		// Base providers (not domain modules).
		fx.Provide(
			validation.Default,
			loadConfig,
			newLogger,
			newTokens,
			newSessions,
			newBus,
			newCache,
			// Outbound seams the account module needs. Which vendor each one talks to is
			// configuration, not code: see the selectors in internal/config.
			newNotifier,
			newOAuthVerifier,
			newKYCClient,
			newPaymentClient,
			// Durable execution: the client modules submit runs through, the server the
			// runtime invokes, and the sweeper that catches whatever a run missed.
			newWorkflowClient,
			fx.Annotate(newWorkflowServer, fx.ParamTags(``, ``, `group:"restate-definitions"`)),
			fx.Annotate(newSweeper, fx.ParamTags(``, ``, `group:"sweeps"`)),
		),
		// Domain modules — each wires its own service + repository.
		account.Module,
		catalog.Module,
		order.Module,
		chat.Module,
		finance.Module,
		trust.Module,
		// Analytics + observability (product events, HTTP/runtime metrics into TimescaleDB).
		observability.Module,
		// Transport.
		gateway.Module,
		// Before the server accepts traffic: marshalling an id without a cipher
		// panics, and fx runs every Invoke before OnStart hooks.
		fx.Invoke(installIDCipher),
		// Eager: nothing in the graph depends on a timer running, so without this the
		// clocks would only start when something happened to ask for them.
		fx.Invoke(startDurable),
		// Route fx's own logs through slog.
		fx.WithLogger(func(log *slog.Logger) fxevent.Logger {
			return &fxevent.SlogLogger{Logger: log}
		}),
	).Run()
}

func loadConfig(v *validator.Validate) (*config.Config, error) {
	return config.Load(v)
}

func newLogger(cfg *config.Config) *slog.Logger {
	return logger.New(logger.Options{Level: cfg.LogLevel, Service: "gateway"})
}

// Two lifetimes, and they are not the same thing. The access token is short because it
// is checked by signature on every request and is only as revocable as the session it
// names; the session is long because it is what "stay signed in" means, and revoking it
// is one key delete.
const (
	accessTokenTTL = 15 * time.Minute
	sessionTTL     = 30 * 24 * time.Hour
)

func newTokens(cfg *config.Config) *token.Manager {
	return token.NewManager(cfg.JWTSecret, accessTokenTTL)
}

func newSessions(c cache.Client) *session.Store {
	return session.New(c, sessionTTL)
}

// The outbound seams. Each is selected by a required env var, so a deployment says out
// loud which vendor it talks to and a local stack can still run on mocks without an SMTP
// account, an SMS contract and a KYC subscription.
//
// Every real client gets an instrumented transport: one RoundTripper layer covers every
// call it makes, which is where outbound RED metrics belong (see the outbound
// cross-cutting rule). None of them sets http.Client.Timeout — the per-operation budgets
// live on the request context instead.

func newNotifier(cfg *config.Config, log *slog.Logger, metrics *observability.Sink) (notify.Client, error) {
	mock := notifymock.NewClient(log)

	var email notify.EmailSender = mock
	if cfg.EmailProvider == "smtp" {
		client, err := smtpnotify.NewClient(smtpnotify.Config{
			Host:             cfg.SMTPHost,
			Port:             cfg.SMTPPort,
			Username:         cfg.SMTPUsername,
			Password:         cfg.SMTPPassword,
			From:             cfg.SMTPFrom,
			VerifyEmailURL:   cfg.VerifyEmailURL,
			ResetPasswordURL: cfg.ResetPasswordURL,
			Timeout:          cfg.SMTPTimeout,
		})
		if err != nil {
			return nil, err
		}
		email = client
	}

	var sms notify.SMSSender = mock
	if cfg.SMSProvider == "esms" {
		client, err := esms.NewClient(esms.Config{
			BaseURL:         cfg.ESMSBaseURL,
			APIKey:          cfg.ESMSAPIKey,
			SecretKey:       cfg.ESMSSecretKey,
			Brandname:       cfg.ESMSBrandname,
			SMSType:         cfg.ESMSSMSType,
			ContentTemplate: cfg.ESMSContentTemplate,
			Unicode:         cfg.ESMSUnicode,
			Sandbox:         cfg.ESMSSandbox,
			Timeout:         cfg.ESMSTimeout,
			HTTPClient:      observedClient("esms", log, metrics),
		})
		if err != nil {
			return nil, err
		}
		sms = client
	}
	return notify.Route(email, sms), nil
}

func newOAuthVerifier(cfg *config.Config, log *slog.Logger, metrics *observability.Sink) (oauth.Verifier, error) {
	if cfg.OAuthVerifier != "oidc" {
		return oauthmock.NewVerifier(), nil
	}
	// A provider with no audience configured is not offered at all: an empty client id
	// would accept a token issued to anybody.
	issuers := map[string]oidcverify.Issuer{}
	if len(cfg.OIDCGoogleAudiences) > 0 {
		issuers["google"] = oidcverify.Issuer{URL: "https://accounts.google.com", Audiences: cfg.OIDCGoogleAudiences}
	}
	if len(cfg.OIDCAppleAudiences) > 0 {
		issuers["apple"] = oidcverify.Issuer{URL: "https://appleid.apple.com", Audiences: cfg.OIDCAppleAudiences}
	}
	return oidcverify.NewVerifier(oidcverify.Config{
		Issuers:    issuers,
		Timeout:    cfg.OIDCTimeout,
		HTTPClient: observedClient("oidc", log, metrics),
	})
}

func newKYCClient(cfg *config.Config, log *slog.Logger, metrics *observability.Sink) (kyc.Client, error) {
	if cfg.KYCProvider != "fpt-ai" {
		return kycmock.NewClient(), nil
	}
	return fptai.NewClient(fptai.Config{
		BaseURL:         cfg.FPTAIBaseURL,
		APIKey:          cfg.FPTAIAPIKey,
		RequestTimeout:  cfg.FPTAIRequestTimeout,
		DownloadTimeout: cfg.FPTAIDownloadTimeout,
		HTTPClient:      observedClient("fpt-ai", log, metrics),
	})
}

// newPaymentClient picks the rail. Only the mock exists today; a real gateway is a new
// case here plus its credentials in config, exactly like the other seams.
func newPaymentClient(cfg *config.Config) payment.Client {
	return paymentmock.NewClient(provider.Option{Provider: cfg.PaymentProvider})
}

// observedClient builds the HTTP client a provider uses: metrics when the telemetry sink
// is wired, and a log line either way. Deliberately no Timeout — that budget covers
// reading the body, so it truncates a download the provider is still streaming.
func observedClient(provider string, log *slog.Logger, metrics *observability.Sink) *http.Client {
	observe := httpx.LogOutbound(log)
	if metrics != nil {
		observe = metrics.OutboundObserver()
	}
	return &http.Client{Transport: httpx.ObserveOutbound(provider, nil, observe)}
}

func installIDCipher(cfg *config.Config) error {
	if err := id.SetCipher([]byte(cfg.IDCipherKey)); err != nil {
		return fmt.Errorf("install id cipher: %w", err)
	}
	return nil
}

func redisClient(cfg *config.Config) (rueidis.Client, error) {
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{cfg.RedisAddr},
		Password:    cfg.RedisPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("create redis client: %w", err)
	}
	return client, nil
}

// newBus provides the event bus backed by Redis Streams. It owns its own
// rueidis client and closes it on shutdown.
func newBus(lc fx.Lifecycle, cfg *config.Config, log *slog.Logger) (eventbus.Client, error) {
	rdb, err := redisClient(cfg)
	if err != nil {
		return nil, err
	}
	b := eventbus.NewRedis(rdb, log)
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return b.Close() }})
	return b, nil
}

// newCache provides the Redis-backed cache. It owns its own rueidis client and
// closes it on shutdown.
func newCache(lc fx.Lifecycle, cfg *config.Config) (cache.Client, error) {
	rdb, err := redisClient(cfg)
	if err != nil {
		return nil, err
	}
	c, err := cache.NewRedisStructClient(rdb, cache.Config{})
	if err != nil {
		rdb.Close()
		return nil, fmt.Errorf("init redis cache: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return c.Close() }})
	return c, nil
}

// --- durable execution ---
//
// Two mechanisms, deliberately: a Restate run per entity is prompt and survives a restart,
// and the sweeper is the periodic net under a run that was lost. Both call the same
// idempotent service methods, so neither is a second definition of "due".

// newWorkflowClient is what modules submit and signal runs through. Nil when no runtime is
// configured, which is why every consumer takes it as an optional dependency: a graph that
// could not be built without a Restate URL would make the runtime mandatory by accident.
func newWorkflowClient(cfg *config.Config, log *slog.Logger, metrics *observability.Sink) *durable.Client {
	if cfg.WorkflowRuntime != "restate" {
		return nil
	}
	// The ingress is an outbound call like any provider's, so it goes through the same observed
	// transport rather than the SDK's default http.DefaultClient.
	return durable.NewClient(cfg.RestateIngressURL, cfg.RestateSendTimeout,
		observedClient("restate", log, metrics), log)
}

// newWorkflowServer hosts the handlers the runtime invokes. Nil when there are no
// definitions, which is the same condition as having no runtime.
func newWorkflowServer(cfg *config.Config, log *slog.Logger, definitions []restate.ServiceDefinition) *durable.Server {
	if cfg.WorkflowRuntime != "restate" || len(definitions) == 0 {
		return nil
	}
	return durable.NewServer(cfg.RestateServeAddr, log, definitions...)
}

// newSweeper collects every module's catch-up pass onto one interval.
func newSweeper(cfg *config.Config, log *slog.Logger, sweeps []durable.Sweep) *durable.Sweeper {
	return durable.NewSweeper(cfg.SweepInterval, log, sweeps...)
}

// startDurable runs both on the process's own lifetime. The context is cancelled on shutdown,
// which is what stops the sweeper's ticker and the handler server.
//
// A handler server that cannot listen takes the process down. It only exists under
// WORKFLOW_RUNTIME=restate, so a deployment that keeps serving without it has silently fallen
// back to the sweep for every timer — slower by minutes, and nothing but this line would say so.
func startDurable(lc fx.Lifecycle, sd fx.Shutdowner, server *durable.Server, sweeper *durable.Sweeper, log *slog.Logger) {
	ctx, stop := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			if server != nil {
				go func() {
					if err := server.Serve(ctx); err != nil && ctx.Err() == nil {
						log.Error("restate handler server stopped", "err", err)
						if err := sd.Shutdown(fx.ExitCode(1)); err != nil {
							log.Error("shutdown after restate server failure", "err", err)
						}
					}
				}()
			}
			go sweeper.Run(ctx)
			return nil
		},
		OnStop: func(context.Context) error {
			stop()
			return nil
		},
	})
}

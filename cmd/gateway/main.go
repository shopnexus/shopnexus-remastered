package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/redis/rueidis"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"shopnexus/internal/config"
	"shopnexus/internal/gateway"
	"shopnexus/internal/infra/cache"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/account"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/chat"
	"shopnexus/internal/module/finance"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/module/order"
	"shopnexus/internal/module/trust"
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

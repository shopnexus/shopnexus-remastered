package account

import (
	"log/slog"

	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
	"shopnexus-server/internal/infras/rankedset"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountconfig "shopnexus-server/internal/module/account/config"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountecho "shopnexus-server/internal/module/account/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the account module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("account",
	fx.Provide(
		func(c *accountconfig.Config) *slog.Logger { return infra.NewLogger(c.Log, "account") },
		func(c *accountconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return infra.NewPool(c.Postgres, lc)
		},
		func(c *accountconfig.Config, lc fx.Lifecycle) (cache.Client, error) {
			return infra.NewCache(c.Redis, lc)
		},
		func(c *accountconfig.Config, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
			return infra.NewBus(c.Bus, c.Redis, logger, lc)
		},
		func(c *accountconfig.Config, lc fx.Lifecycle) (rankedset.Client, error) {
			return infra.NewRankedSet(c.RankedSet, c.Redis, lc)
		},
		fx.Private,
	),
	fx.Provide(
		accountconfig.NewConfig,
		NewAccountStorage,
		accountbiz.NewAccountHandler,
		NewAccountBiz,
		accountecho.NewHandler,
	),
	fx.Provide(
		fx.Annotate(
			func(b *accountbiz.AccountHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(accountbiz.NewAccountService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *accountbiz.AccountHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { accountbiz.RegisterAccountBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
	),
	fx.Invoke(
		accountecho.NewHandler,
		WireClaimsSecret,
	),
)

// WireClaimsSecret installs the JWT access-token secret into the shared
// authclaims package so GetClaims(r) (called by every transport handler) can
// validate tokens without an injected dep.
// TODO: nghĩ cách khác để viết về auth
func WireClaimsSecret(cfg *accountconfig.Config) {
	authclaims.SetSecret(cfg.JWT.Secret)
}

// NewAccountStorage creates a new account storage backed by PostgreSQL.
func NewAccountStorage(pool pgsqlc.TxBeginner) accountbiz.AccountStorage {
	return pgsqlc.NewStorage(pool, accountdb.New(pool))
}

// NewAccountBiz creates the account client. The AccountHandler injects its own
// AccountBizClient, so InProcess (client wraps handler) would form an fx cycle.
// Account therefore uses the Remote client pointing at the BestEffort server.
func NewAccountBiz(cfg *accountconfig.Config) accountbiz.AccountBizClient {
	return accountbiz.NewAccountBizClientRemote(cfg.Restate.IngressAddress, cfg.Restate.BestEffortAddress)
}

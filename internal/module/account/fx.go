package account

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/infra"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountecho "shopnexus-server/internal/module/account/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the account module. Infra (pool/cache/bus/rankedset/logger) is
// its own fx.Private set via infra.StandardModule, built from the shared config.
var Module = fx.Module("account",
	infra.StandardModule("account"),
	fx.Provide(
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
func WireClaimsSecret(cfg *config.Config) {
	authclaims.SetSecret(cfg.JWT.Secret)
}

// NewAccountStorage creates a new account storage backed by PostgreSQL.
func NewAccountStorage(pool pgsqlc.TxBeginner) accountbiz.AccountStorage {
	return pgsqlc.NewStorage(pool, accountdb.New(pool))
}

// NewAccountBiz creates the account client. The AccountHandler injects its own
// AccountBizClient, so InProcess (client wraps handler) would form an fx cycle.
// Account therefore uses the Remote client pointing at the BestEffort server.
func NewAccountBiz(cfg *config.Config) accountbiz.AccountBizClient {
	return accountbiz.NewAccountBizClientRemote(cfg.Restate.IngressAddress, cfg.Restate.BestEffortAddress)
}

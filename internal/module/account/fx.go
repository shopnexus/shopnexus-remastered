package account

import (
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountconfig "shopnexus-server/internal/module/account/config"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountecho "shopnexus-server/internal/module/account/transport/echo"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the account module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("account",
	fxinfra.Providers[*accountconfig.Config]("account"),
	fx.Provide(
		accountconfig.NewConfig,
		NewAccountStorage,
		accountbiz.NewAccountHandler,
		NewAccountBiz,
		accountecho.NewHandler,
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

// NewAccountBiz creates a Restate-backed client for the account module.
func NewAccountBiz(cfg *accountconfig.Config) accountbiz.AccountBizClient {
	return accountbiz.NewAccountBizClientRemote(cfg.Restate.IngressAddress, cfg.Restate.IngressAddress)
}

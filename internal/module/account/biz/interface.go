package accountbiz

import (
	"context"
	"time"

	"shopnexus-server/config"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// AccountBiz is the client interface for AccountBizHandler, which is used by other modules to call AccountBizHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface AccountBiz -service Account
type AccountBiz interface {
	// Auth
	Login(ctx restate.Context, params LoginParams) (LoginResult, error)
	Register(ctx restate.Context, params RegisterParams) (RegisterResult, error)
	Refresh(ctx restate.Context, refreshToken string) (RefreshResult, error)

	// Profile
	GetProfile(ctx context.Context, params GetProfileParams) (accountmodel.Profile, error)
	ListProfile(ctx context.Context, params ListProfileParams) (paginate.PaginateResult[accountmodel.Profile], error)
	UpdateProfile(ctx restate.Context, params UpdateProfileParams) (accountmodel.Profile, error)
	UpdateCountry(ctx restate.Context, params UpdateCountryParams) error

	// Internal wallet (profile.internal_balance)
	GetWalletBalance(ctx context.Context, accountID uuid.UUID) (int64, error)
	WalletDebit(ctx restate.Context, params WalletDebitParams) (WalletDebitResult, error)
	WalletCredit(ctx restate.Context, params WalletCreditParams) error

	// Account
	SuspendAccount(ctx restate.Context, params SuspendAccountParams) error

	// Contact
	ListContact(ctx context.Context, params ListContactParams) ([]accountdb.AccountContact, error)
	GetContact(ctx context.Context, params GetContactParams) (accountdb.AccountContact, error)
	CreateContact(ctx restate.Context, params CreateContactParams) (accountdb.AccountContact, error)
	UpdateContact(ctx restate.Context, params UpdateContactParams) (accountdb.AccountContact, error)
	DeleteContact(ctx restate.Context, params DeleteContactParams) error
	GetDefaultContact(ctx context.Context, accountIDs []uuid.UUID) (map[uuid.UUID]accountdb.AccountContact, error)

	// Favorite
	AddFavorite(ctx restate.Context, params AddFavoriteParams) (accountdb.AccountFavorite, error)
	RemoveFavorite(ctx restate.Context, params RemoveFavoriteParams) error
	ListFavorite(
		ctx context.Context,
		params ListFavoriteParams,
	) (paginate.PaginateResult[accountdb.AccountFavorite], error)
	CheckFavorites(ctx context.Context, params CheckFavoritesParams) (map[uuid.UUID]bool, error)

	// Notification
	ListNotification(
		ctx context.Context,
		params ListNotificationParams,
	) (paginate.PaginateResult[accountdb.AccountNotification], error)
	CountUnread(ctx context.Context, params CountUnreadParams) (int64, error)
	MarkRead(ctx restate.Context, params MarkReadParams) error
	MarkAllRead(ctx restate.Context, params MarkAllReadParams) error
	CreateNotification(ctx restate.Context, params CreateNotificationParams) (accountdb.AccountNotification, error)
}

type AccountStorage = pgsqlc.Storage[*accountdb.Queries]

// AccountHandler implements the core business logic for the account module.
type AccountHandler struct {
	tokenDuration        time.Duration
	jwtSecret            []byte
	refreshTokenDuration time.Duration
	refreshSecret        []byte

	storage AccountStorage
	common  commonbiz.CommonBizClient
	self    AccountBizClient // self-signals (e.g. welcome notification) stay async via Restate
}

func (b *AccountHandler) ServiceName() string {
	return "Account"
}

// NewAccountHandler creates a new AccountHandler with the given dependencies.
func NewAccountHandler(
	cfg *config.Config,
	storage AccountStorage,
	common commonbiz.CommonBizClient,
	self AccountBizClient,
) *AccountHandler {
	return &AccountHandler{
		tokenDuration:        time.Duration(cfg.JWT.AccessTokenDuration * int64(time.Second)),
		jwtSecret:            []byte(cfg.JWT.Secret),
		refreshTokenDuration: time.Duration(cfg.JWT.RefreshTokenDuration * int64(time.Second)),
		refreshSecret:        []byte(cfg.JWT.RefreshSecret),

		storage: storage,
		common:  common,
		self:    self,
	}
}

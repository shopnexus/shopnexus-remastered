package commonbiz

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"shopnexus-server/config"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/provider/exchange"
	"shopnexus-server/internal/provider/geocoding"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// CommonBiz is the interface for common module, used by other modules.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface CommonBiz -service Common
type CommonBiz interface {
	// File
	GetFileURL(ctx context.Context, params GetFileURLParams) (string, error)

	// Option
	ListOption(ctx context.Context, params ListOptionParams) ([]OptionListItem, error)
	UpsertOptions(ctx restate.Context, params UpsertOptionsParams) error
	DeleteOptions(ctx restate.Context, params DeleteOptionParams) error

	// Resource
	UpdateResources(ctx restate.Context, params UpdateResourcesParams) ([]commonmodel.Resource, error)
	DeleteResources(ctx restate.Context, params DeleteResourcesParams) error
	GetResources(ctx context.Context, params GetResourcesParams) (map[uuid.UUID][]commonmodel.Resource, error)
	GetResourcesByIDs(ctx context.Context, resourceIDs []uuid.UUID) (map[uuid.UUID]commonmodel.Resource, error)
	GetResourceByID(ctx context.Context, resourceID uuid.UUID) (*commonmodel.Resource, error)

	// Geocoding
	ReverseGeocode(ctx context.Context, params ReverseGeocodeParams) (geocoding.Result, error)
	ForwardGeocode(ctx context.Context, params ForwardGeocodeParams) (geocoding.Result, error)
	SearchGeocode(ctx context.Context, params SearchGeocodeParams) ([]geocoding.Result, error)
	ResolveCountry(ctx context.Context, address string) (string, error)

	// SSE
	PushEvent(ctx restate.Context, params PushEventParams) error

	// Exchange rates
	GetExchangeRates(ctx context.Context, params GetExchangeRatesParams) (commonmodel.ExchangeRateSnapshot, error)
	ConvertAmount(ctx context.Context, params ConvertAmountParams) (int64, error)
	IsSupportedCurrency(ctx context.Context, currency string) (bool, error)
}

type CommonStorage = pgsqlc.Storage[*commondb.Queries]

// SSEClient is a connected SSE client with a buffered channel.
type SSEClient struct {
	Ch chan []byte
}

// CommonHandler implements shared business logic used across modules.
type CommonHandler struct {
	cfg      *config.Config
	logger   *slog.Logger
	storage  CommonStorage
	geocoder geocoding.Client
	exchange exchange.Client

	// SSE client registry
	sseMu      sync.RWMutex
	sseClients map[uuid.UUID][]*SSEClient
}

func (b *CommonHandler) ServiceName() string {
	return "Common"
}

// NewcommonBiz creates a new CommonHandler with the given dependencies.
func NewcommonBiz(
	cfg *config.Config,
	logger *slog.Logger,
	storage CommonStorage,
	geocoder geocoding.Client,
	exchangeClient exchange.Client,
) (*CommonHandler, error) {
	b := &CommonHandler{
		cfg:        cfg,
		logger:     logger,
		storage:    storage,
		geocoder:   geocoder,
		exchange:   exchangeClient,
		sseClients: make(map[uuid.UUID][]*SSEClient),
	}

	return b, errors.Join(
		b.SetupObjectStore(),
	)
}

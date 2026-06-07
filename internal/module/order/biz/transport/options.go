package transport

import (
	"context"
	"log/slog"

	commonbiz "shopnexus-server/internal/module/common/biz"
	sharedmodel "shopnexus-server/internal/shared/model"
)

// SetupTransportMap registers the transport options in the central catalog.
// Clients themselves are built on demand — nothing is cached on the handler.
func (b *TransportHandler) SetupTransportMap() error {
	configs := b.TransportOptions()

	go func() {
		if err := b.common.Send().UpsertOptions(context.Background(), commonbiz.UpsertOptionsParams{
			Type:    string(sharedmodel.OptionTypeTransport),
			Configs: configs,
		}); err != nil {
			b.Logger.Warn("register transport options", slog.Any("error", err))
		}
	}()

	return nil
}

package payment

import (
	"context"
	"log/slog"

	commonbiz "shopnexus-server/internal/module/common/biz"
	sharedmodel "shopnexus-server/internal/shared/model"
)

// SetupPaymentMap registers the payment options in the central catalog.
// Clients themselves are built on demand — nothing is cached on the handler.
func (b *PaymentHandler) SetupPaymentMap() error {
	configs := b.PaymentConfigs()

	go func() {
		if err := b.common.Send().UpsertOptions(context.Background(), commonbiz.UpsertOptionsParams{
			Type:    string(sharedmodel.OptionTypePayment),
			Configs: configs,
		}); err != nil {
			b.Logger.Warn("register payment options", slog.Any("error", err))
		}
	}()

	return nil
}

package base

import (
	"encoding/json"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	"shopnexus-server/internal/provider/payment/card"
	"shopnexus-server/internal/provider/payment/sepay"
	"shopnexus-server/internal/provider/payment/vnpay"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/provider/transport/ghtk"
	sharedmodel "shopnexus-server/internal/shared/model"
)

// paymentFactory routes a payment Option to its provider-specific constructor.
func (b *Base) paymentFactory(cfg sharedmodel.Option) payment.Client {
	switch cfg.Provider {
	case "vnpay":
		return vnpay.NewClient(cfg)
	case "sepay":
		return sepay.NewClient(cfg)
	case "card":
		return card.NewClient(cfg)
	default:
		b.Logger.Warn("unknown payment provider", "provider", cfg.Provider, "id", cfg.ID)
		return nil
	}
}

func (b *Base) PaymentConfigs() []sharedmodel.Option {
	var configs []sharedmodel.Option

	returnURL := b.Cfg.Order.ReturnURL

	vnpayCfg := b.Cfg.Vnpay
	for _, method := range []string{vnpay.MethodQR, vnpay.MethodBank, vnpay.MethodATM} {
		data, _ := json.Marshal(vnpay.Data{
			TmnCode:    vnpayCfg.TmnCode,
			HashSecret: vnpayCfg.HashSecret,
			ReturnURL:  returnURL,
			Method:     method,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "vnpay_" + method,
			Type:     sharedmodel.OptionTypePayment,
			Provider: "vnpay",
			Name:     "VNPay - " + method,
			Data:     data,
		})
	}

	if c := b.Cfg.Sepay; c.MerchantID != "" {
		data, _ := json.Marshal(sepay.Data{
			MerchantID:    c.MerchantID,
			SecretKey:     c.SecretKey,
			IPNSecretKey:  c.IPNSecretKey,
			PublicBaseURL: c.PublicBaseURL,
			ReturnURL:     returnURL,
			Sandbox:       c.Sandbox,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "sepay_bank_transfer",
			Type:     sharedmodel.OptionTypePayment,
			Provider: "sepay",
			Name:     "SePay - Bank Transfer",
			Data:     data,
		})
	}

	if c := b.Cfg.CardPayment; c.Provider != "" {
		data, _ := json.Marshal(card.Data{
			Processor: c.Provider,
			SecretKey: c.SecretKey,
			PublicKey: c.PublicKey,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "card_" + c.Provider,
			Type:     sharedmodel.OptionTypePayment,
			Provider: "card",
			Name:     "Card Payment (" + c.Provider + ")",
			Data:     data,
		})
	}

	return configs
}

// GetPaymentClient builds a payment client on demand for the given option ID.
// The lookup walks the config-derived option list — no per-handler cache.
func (b *Base) GetPaymentClient(option string) (payment.Client, error) {
	for _, cfg := range b.PaymentConfigs() {
		if cfg.ID == option {
			if client := b.paymentFactory(cfg); client != nil {
				return client, nil
			}
			break
		}
	}
	return nil, ordermodel.ErrUnknownPaymentOption.Fmt(option)
}

// transportFactory routes a transport Option to its provider-specific constructor.
func (b *Base) transportFactory(cfg sharedmodel.Option) transport.Client {
	switch cfg.Provider {
	case "ghtk":
		return ghtk.NewClient(cfg)
	default:
		b.Logger.Warn("unknown transport provider", "provider", cfg.Provider, "id", cfg.ID)
		return nil
	}
}

func (b *Base) TransportOptions() []sharedmodel.Option {
	var configs []sharedmodel.Option

	ghtkCfg := b.Cfg.GHTK
	for _, method := range []string{ghtk.ServiceExpress, ghtk.ServiceStandard, ghtk.ServiceEconomy} {
		data, _ := json.Marshal(ghtk.Data{
			Method:   method,
			BaseURL:  ghtkCfg.BaseURL,
			APIKey:   ghtkCfg.APIKey,
			ClientID: ghtkCfg.ClientID,
			Secret:   ghtkCfg.Secret,
		})
		configs = append(configs, sharedmodel.Option{
			ID:          "ghtk_" + method,
			Type:        sharedmodel.OptionTypeTransport,
			Provider:    "ghtk",
			Name:        "Giao hàng tiết kiệm - " + method,
			Description: "Dịch vụ giao hàng nhanh của Giao hàng tiết kiệm",
			Data:        data,
		})
	}

	return configs
}

// GetTransportClient builds a transport client on demand for the given option ID.
// The lookup walks the config-derived option list — no per-handler cache.
func (b *Base) GetTransportClient(option string) (transport.Client, error) {
	for _, cfg := range b.TransportOptions() {
		if cfg.ID == option {
			if client := b.transportFactory(cfg); client != nil {
				return client, nil
			}
			break
		}
	}
	return nil, ordermodel.ErrUnknownTransportOption
}

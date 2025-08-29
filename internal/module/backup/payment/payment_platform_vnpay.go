package payment

import (
	"context"

	"shopnexus-go-service/config"
	"shopnexus-go-service/internal/client/vnpay"
)

type VnpayPlatform struct {
	client vnpay.Client
}

func (p *VnpayPlatform) CreateOrder(ctx context.Context, params CreateOrderParams) (url string, err error) {
	return p.client.CreateOrder(ctx, vnpay.CreateOrderParams{
		PaymentID: params.PaymentID,
		Amount:    params.Amount,
		Info:      params.Info,
		ReturnUrl: config.GetConfig().App.FrontendUrl + "/payment-resolve",
	})
}

package commonbiz

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	commonmodel "shopnexus-server/internal/module/common/model"
)

// GetExchangeRatesParams is an empty envelope required by the Restate
// ingress client — zero-arg handlers reject requests with a JSON
// content-type, so we always send an empty object.
type GetExchangeRatesParams struct{}

// ConvertAmountParams: amount in smallest unit of From, converted to
// smallest unit of To.
type ConvertAmountParams struct {
	Amount   int64
	From, To string
}

// ConvertAmountPure converts amount through the USD base. ratesFromUSD
// maps target currency to "1 USD = rate target". Returns the original
// amount unchanged when a rate is missing (fail-open; callers display
// original currency). Exported for testability without cache setup.
func ConvertAmountPure(amount int64, from, to string, ratesFromUSD map[string]decimal.Decimal) int64 {
	if from == to {
		return amount
	}
	one := decimal.NewFromInt(1)
	rateFrom := one
	if from != "USD" {
		r, ok := ratesFromUSD[from]
		if !ok || r.Sign() <= 0 {
			return amount
		}
		rateFrom = r
	}
	rateTo := one
	if to != "USD" {
		r, ok := ratesFromUSD[to]
		if !ok || r.Sign() <= 0 {
			return amount
		}
		rateTo = r
	}
	decFrom := decimalsFor(from)
	decTo := decimalsFor(to)
	// amount * 10^-decFrom (major units of `from`) / rateFrom (major USD)
	// * rateTo (major units of `to`) * 10^decTo (minor units of `to`).
	return decimal.NewFromInt(amount).
		Shift(int32(-decFrom)).
		Div(rateFrom).
		Mul(rateTo).
		Shift(int32(decTo)).
		Round(0).
		IntPart()
}

// GetExchangeRates returns the latest snapshot. The exchange.Client is a
// read-through cache, so this hits the upstream provider only on the first
// lookup after the TTL expires. On any error (no provider, upstream down)
// it returns an empty Rates map with correct metadata — callers fail-open.
func (b *CommonHandler) GetExchangeRates(ctx context.Context, _ GetExchangeRatesParams) (commonmodel.ExchangeRateSnapshot, error) {
	base := b.cfg.Exchange.Base
	snap := commonmodel.ExchangeRateSnapshot{
		Base:      base,
		Rates:     map[string]decimal.Decimal{},
		Supported: b.cfg.Exchange.Supported,
	}

	if b.exchange == nil {
		b.logger.Warn("exchange: no provider configured", "base", base)
		return snap, nil
	}

	targets := make([]string, 0, len(b.cfg.Exchange.Supported))
	for _, c := range b.cfg.Exchange.Supported {
		if c != base {
			targets = append(targets, c)
		}
	}

	fetched, err := b.exchange.FetchLatest(ctx, base, targets)
	if err != nil {
		b.logger.Warn("exchange fetch failed", "base", base, "err", err)
		return snap, nil
	}

	// Provider returns float64 (JSON wire format). Convert to decimal at the
	// boundary so all internal compute is exact.
	rates := make(map[string]decimal.Decimal, len(fetched.Rates))
	for k, v := range fetched.Rates {
		rates[k] = decimal.NewFromFloat(v)
	}
	snap.Rates = rates
	snap.FetchedAt = fetched.FetchedAt
	return snap, nil
}

// ConvertAmount: BE helper for cross-currency math (filter, analytics).
func (b *CommonHandler) ConvertAmount(ctx context.Context, p ConvertAmountParams) (int64, error) {
	snap, err := b.GetExchangeRates(ctx, GetExchangeRatesParams{})
	if err != nil {
		return 0, err
	}
	return ConvertAmountPure(p.Amount, p.From, p.To, snap.Rates), nil
}

// IsSupportedCurrency checks against the config whitelist.
// Returns an error tuple to conform to the Restate proxy calling convention
// for interface methods; lookup itself never fails.
func (b *CommonHandler) IsSupportedCurrency(_ context.Context, currency string) (bool, error) {
	return slices.Contains(b.cfg.Exchange.Supported, currency), nil
}

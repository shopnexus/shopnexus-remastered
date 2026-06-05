package commonbiz

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/provider/geocoding"

	restate "github.com/restatedev/sdk-go"
)

// forwardGeocodeCacheTTL is long because address -> country mappings rarely
// change; the cache is the primary defence against Nominatim rate limits.
const forwardGeocodeCacheTTL = 30 * 24 * time.Hour

func forwardGeocodeCacheKey(address string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address))))
	return "geocode:forward:" + hex.EncodeToString(sum[:])[:16]
}

type ReverseGeocodeParams struct {
	Latitude  float64 `validate:"required"`
	Longitude float64 `validate:"required"`
}

func (b *CommonHandler) ReverseGeocode(ctx restate.Context, params ReverseGeocodeParams) (geocoding.Result, error) {
	return restate.Run(ctx, func(ctx restate.RunContext) (geocoding.Result, error) {
		result, err := b.geocoder.ReverseGeocode(ctx, params.Latitude, params.Longitude)
		if err != nil {
			if errors.Is(err, geocoding.ErrNoResults) {
				return geocoding.Result{}, commonmodel.ErrAddressNotFound
			}
			return geocoding.Result{}, fmt.Errorf("reverse geocode: %w", err)
		}
		return result, nil
	})
}

type ForwardGeocodeParams struct {
	Address string `validate:"required"`
}

// ForwardGeocode resolves a free-form address to coordinates and country.
// Cached by lowercased address for 30 days to limit Nominatim load and stay
// within its 1 req/sec public rate limit.
func (b *CommonHandler) ForwardGeocode(ctx restate.Context, params ForwardGeocodeParams) (geocoding.Result, error) {
	return restate.Run(ctx, func(ctx restate.RunContext) (geocoding.Result, error) {
		cacheKey := forwardGeocodeCacheKey(params.Address)

		var cached geocoding.Result
		if err := b.cache.Get(ctx, cacheKey, &cached); err == nil && cached.Address != "" {
			return cached, nil
		}

		result, err := b.geocoder.ForwardGeocode(ctx, params.Address)
		if err != nil {
			if errors.Is(err, geocoding.ErrNoResults) {
				return geocoding.Result{}, commonmodel.ErrAddressNotFound
			}
			return geocoding.Result{}, fmt.Errorf("forward geocode: %w", err)
		}

		if result.Address != "" {
			if err := b.cache.Set(ctx, cacheKey, result, forwardGeocodeCacheTTL); err != nil {
				b.logger.Warn("forward geocode cache set failed", "err", err)
			}
		}

		return result, nil
	})
}

// ResolveCountry geocodes the address and returns the ISO 3166-1 alpha-2
// country code (uppercase). Returns a terminal 400 error if the address is
// blank, geocoding fails, or no country was resolved.
func (b *CommonHandler) ResolveCountry(ctx restate.Context, address string) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", commonmodel.ErrEmptyAddress
	}
	result, err := b.ForwardGeocode(ctx, ForwardGeocodeParams{Address: address})
	if err != nil {
		return "", fmt.Errorf("resolve address country: %w", err)
	}
	if result.CountryCode == "" {
		return "", commonmodel.ErrAddressCountryUnresolved
	}
	return result.CountryCode, nil
}

type SearchGeocodeParams struct {
	Query string `validate:"required,min=2"`
	Limit int
}

func (b *CommonHandler) SearchGeocode(ctx restate.Context, params SearchGeocodeParams) ([]geocoding.Result, error) {
	return restate.Run(ctx, func(ctx restate.RunContext) ([]geocoding.Result, error) {
		results, err := b.geocoder.Search(ctx, params.Query, params.Limit)
		if err != nil {
			return nil, fmt.Errorf("search geocode: %w", err)
		}
		return results, nil
	})
}

package commonbiz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/provider/geocoding"
)

type ReverseGeocodeParams struct {
	Latitude  float64 `validate:"required"`
	Longitude float64 `validate:"required"`
}

func (b *CommonHandler) ReverseGeocode(ctx context.Context, params ReverseGeocodeParams) (geocoding.Result, error) {
	result, err := b.geocoder.ReverseGeocode(ctx, params.Latitude, params.Longitude)
	if err != nil {
		if errors.Is(err, geocoding.ErrNoResults) {
			return geocoding.Result{}, commonmodel.ErrAddressNotFound
		}
		return geocoding.Result{}, fmt.Errorf("reverse geocode: %w", err)
	}
	return result, nil
}

type ForwardGeocodeParams struct {
	Address string `validate:"required"`
}

// ForwardGeocode resolves a free-form address to coordinates and country.
// The geocoder is wrapped in a read-through cache (see geocoding.CachingClient).
func (b *CommonHandler) ForwardGeocode(ctx context.Context, params ForwardGeocodeParams) (geocoding.Result, error) {
	result, err := b.geocoder.ForwardGeocode(ctx, params.Address)
	if err != nil {
		if errors.Is(err, geocoding.ErrNoResults) {
			return geocoding.Result{}, commonmodel.ErrAddressNotFound
		}
		return geocoding.Result{}, fmt.Errorf("forward geocode: %w", err)
	}
	return result, nil
}

// ResolveCountry geocodes the address and returns the ISO 3166-1 alpha-2
// country code (uppercase). Returns a terminal 400 error if the address is
// blank, geocoding fails, or no country was resolved.
func (b *CommonHandler) ResolveCountry(ctx context.Context, address string) (string, error) {
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

func (b *CommonHandler) SearchGeocode(ctx context.Context, params SearchGeocodeParams) ([]geocoding.Result, error) {
	results, err := b.geocoder.Search(ctx, params.Query, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("search geocode: %w", err)
	}
	return results, nil
}

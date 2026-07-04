package geocoding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

// NominatimClient implements Provider using OpenStreetMap Nominatim (free, 1 req/sec).
type NominatimClient struct {
	client *http.Client
}

func NewNominatimProvider() *NominatimClient {
	return &NominatimClient{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type nominatimResponse struct {
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	Address     struct {
		CountryCode string `json:"country_code"`
	} `json:"address"`
}

func (p *NominatimClient) ReverseGeocode(ctx context.Context, lat, lng float64) (Result, error) {
	var zero Result

	url := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=json&addressdetails=1&lat=%f&lon=%f&zoom=18", lat, lng)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, fmt.Errorf("geocoding: create request: %w", err)
	}
	req.Header.Set("User-Agent", "ShopNexus/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("geocoding: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("geocoding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("geocoding: nominatim returned %d: %s", resp.StatusCode, string(body))
	}

	var result nominatimResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return zero, fmt.Errorf("geocoding: unmarshal: %w", err)
	}

	if result.DisplayName == "" {
		return zero, fmt.Errorf("geocoding: no address found for coordinates %f, %f: %w", lat, lng, ErrNoResults)
	}

	return Result{
		Address:     result.DisplayName,
		CountryCode: strings.ToUpper(result.Address.CountryCode),
		Latitude:    lat,
		Longitude:   lng,
	}, nil
}

type nominatimSearchResponse struct {
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	Address     struct {
		CountryCode string `json:"country_code"`
	} `json:"address"`
}

func (p *NominatimClient) ForwardGeocode(ctx context.Context, address string) (Result, error) {
	var zero Result

	url := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?format=json&addressdetails=1&q=%s&limit=1",
		neturl.QueryEscape(address),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, fmt.Errorf("geocoding: create request: %w", err)
	}
	req.Header.Set("User-Agent", "ShopNexus/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("geocoding: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("geocoding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("geocoding: nominatim returned %d: %s", resp.StatusCode, string(body))
	}

	var results []nominatimSearchResponse
	if err := json.Unmarshal(body, &results); err != nil {
		return zero, fmt.Errorf("geocoding: unmarshal: %w", err)
	}

	if len(results) == 0 {
		return zero, fmt.Errorf("geocoding: no results found for address %q: %w", address, ErrNoResults)
	}

	var lat, lng float64
	_, _ = fmt.Sscanf(results[0].Lat, "%f", &lat)
	_, _ = fmt.Sscanf(results[0].Lon, "%f", &lng)

	return Result{
		Address:     results[0].DisplayName,
		CountryCode: strings.ToUpper(results[0].Address.CountryCode),
		Latitude:    lat,
		Longitude:   lng,
	}, nil
}

func (p *NominatimClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	url := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?format=json&addressdetails=1&q=%s&limit=%d",
		neturl.QueryEscape(query),
		limit,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("geocoding: create request: %w", err)
	}
	req.Header.Set("User-Agent", "ShopNexus/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geocoding: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("geocoding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocoding: nominatim returned %d: %s", resp.StatusCode, string(body))
	}

	var results []nominatimSearchResponse
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("geocoding: unmarshal: %w", err)
	}

	out := make([]Result, 0, len(results))
	for _, r := range results {
		var lat, lng float64
		_, _ = fmt.Sscanf(r.Lat, "%f", &lat)
		_, _ = fmt.Sscanf(r.Lon, "%f", &lng)
		out = append(out, Result{
			Address:     r.DisplayName,
			CountryCode: strings.ToUpper(r.Address.CountryCode),
			Latitude:    lat,
			Longitude:   lng,
		})
	}

	return out, nil
}

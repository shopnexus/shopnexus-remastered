// See: https://docs.giaohangtietkiem.vn/webhook
package ghtk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"shopnexus-server/internal/provider/transport"
	sharedmodel "shopnexus-server/internal/shared/model"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	ServiceExpress  = "express"
	ServiceStandard = "standard"
	ServiceEconomy  = "economy"
)

// Data is the provider-specific config carried in sharedmodel.Option.Data.
type Data struct {
	Method   string `json:"method"` // express, standard, economy
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	ClientID string `json:"client_id"`
	Secret   string `json:"secret"` // HMAC secret for webhook signature verification (optional)
}

// GHTKClient implements the transport.Client interface for GHTK (fake implementation).
type GHTKClient struct {
	config sharedmodel.Option
	data   Data

	transports map[string]*fakeTransport // In-memory storage for fake tracking
}

// fakeTransport represents a fake transport in our mock system.
type fakeTransport struct {
	ID      string
	Service string
	Status  string
	Cost    int64

	FromAddress string
	ToAddress   string

	WeightGrams int32

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ghtkData is stored as JSONB in the transport data field.
type ghtkData struct {
	TrackingID string    `json:"tracking_id"`
	LabelURL   string    `json:"label_url"`
	ETA        time.Time `json:"eta"`
	Location   string    `json:"location,omitempty"`
}

// packageDetails mirrors the shape stored in ItemMetadata.PackageDetails.
type packageDetails struct {
	WeightGrams int32 `json:"weight_grams"`
	LengthCM    int32 `json:"length_cm"`
	WidthCM     int32 `json:"width_cm"`
	HeightCM    int32 `json:"height_cm"`
}

func NewClient(cfg sharedmodel.Option) transport.Client {
	var data Data
	if len(cfg.Data) > 0 {
		_ = json.Unmarshal(cfg.Data, &data)
	}
	return &GHTKClient{
		config:     cfg,
		data:       data,
		transports: make(map[string]*fakeTransport),
	}
}

func (g *GHTKClient) Config() sharedmodel.Option {
	return g.config
}

// Quote calculates estimated cost without creating a transport.
// Weight is extracted from PackageDetails of the first item.
func (g *GHTKClient) Quote(ctx context.Context, params transport.QuoteParams) (transport.QuoteResult, error) {
	weightGrams := g.extractWeight(params.Items)
	cost := g.calculateShippingCost(weightGrams)

	data, err := json.Marshal(ghtkData{
		ETA: g.calculateETA(),
	})
	if err != nil {
		return transport.QuoteResult{}, fmt.Errorf("marshal quote data: %w", err)
	}

	return transport.QuoteResult{
		Cost: int64(cost),
		Data: data,
	}, nil
}

// Create books a transport and returns transport with tracking data in Data JSONB.
func (g *GHTKClient) Create(ctx context.Context, params transport.CreateParams) (transport.Transport, error) {
	trackingID := g.generateTrackingID()
	weightGrams := g.extractWeight(params.Items)
	cost := g.calculateShippingCost(weightGrams)
	eta := g.calculateETA()

	ship := &fakeTransport{
		ID:          trackingID,
		Service:     g.data.Method,
		Status:      "LabelCreated",
		Cost:        cost,
		FromAddress: params.FromAddress,
		ToAddress:   params.ToAddress,
		WeightGrams: weightGrams,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	g.transports[trackingID] = ship

	id, err := uuid.NewRandom()
	if err != nil {
		return transport.Transport{}, fmt.Errorf("generate transport id: %w", err)
	}

	data, err := json.Marshal(ghtkData{
		TrackingID: trackingID,
		LabelURL:   fmt.Sprintf("%s/labels/%s.pdf", g.data.BaseURL, trackingID),
		ETA:        eta,
	})
	if err != nil {
		return transport.Transport{}, fmt.Errorf("marshal transport data: %w", err)
	}

	return transport.Transport{
		ID:     id,
		Option: g.data.Method,
		Cost:   int64(cost),
		Data:   data,
	}, nil
}

// Track returns the current status for a given tracking id.
func (g *GHTKClient) Track(ctx context.Context, id string) (transport.TrackResult, error) {
	ship, exists := g.transports[id]
	if !exists {
		return transport.TrackResult{}, fmt.Errorf("tracking id not found: %s", id)
	}

	g.updateStatus(ship)

	location := g.getCurrentLocation(ship)
	data, err := json.Marshal(ghtkData{
		TrackingID: id,
		Location:   location,
	})
	if err != nil {
		return transport.TrackResult{}, fmt.Errorf("marshal track data: %w", err)
	}

	return transport.TrackResult{
		Status: ship.Status,
		Data:   data,
	}, nil
}

// Cancel attempts to cancel a transport by its tracking id.
func (g *GHTKClient) Cancel(ctx context.Context, id string) error {
	ship, exists := g.transports[id]
	if !exists {
		return fmt.Errorf("tracking id not found: %s", id)
	}

	if ship.Status == "Delivered" {
		return fmt.Errorf("cannot cancel delivered transport: %s", id)
	}

	ship.Status = "Cancelled"
	ship.UpdatedAt = time.Now()
	return nil
}

// ghtkWebhookPayload is the expected JSON body from GHTK status webhooks.
// See: https://docs.giaohangtietkiem.vn/webhook
type ghtkWebhookPayload struct {
	LabelID    string `json:"label_id"`    // GHTK tracking label ID
	StatusID   int    `json:"status_id"`   // GHTK numeric status code
	StatusName string `json:"status_name"` // Human-readable status from GHTK
	PartnerID  string `json:"partner_id"`  // Our partner/client ID
	OrderID    string `json:"order_id"`    // Provider-side order ID
}

// mapGHTKStatus converts a GHTK numeric status_id to our OrderTransportStatus string.
func mapGHTKStatus(statusID int) string {
	switch statusID {
	case 1, 2:
		return "LabelCreated"
	case 3, 4, 5:
		return "InTransit"
	case 6:
		return "OutForDelivery"
	case 7, 45:
		return "Delivered"
	case 9, 12, 13:
		return "Failed"
	case 8, 11:
		return "Cancelled"
	default:
		return ""
	}
}

// WireWebhooks registers the GHTK webhooks route on Echo.
// Route: POST /api/v1/transport/webhook/ghtk
// All service variants (express/standard/economy) share the same endpoint;
// the caller's `registered` map collapses duplicate registrations.
func (g *GHTKClient) WireWebhooks(e *echo.Echo, deliver transport.ResultHandler, registered map[string]struct{}) string {
	const key = "transport/ghtk"
	if _, ok := registered[key]; ok {
		return key
	}

	e.POST("/api/v1/transport/webhook/ghtk", func(ec echo.Context) error {
		// Verify HMAC-SHA256 signature if secret is configured
		if g.data.Secret != "" {
			body, err := io.ReadAll(ec.Request().Body)
			if err != nil {
				return ec.NoContent(http.StatusBadRequest)
			}
			ec.Request().Body = io.NopCloser(bytes.NewReader(body))

			sig := ec.Request().Header.Get("X-Ghtk-Signature")
			mac := hmac.New(sha256.New, []byte(g.data.Secret))
			mac.Write(body)
			expected := hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(expected)) {
				slog.Warn("ghtk webhook: invalid signature")
				return ec.NoContent(http.StatusUnauthorized)
			}
		}

		var payload ghtkWebhookPayload
		if err := ec.Bind(&payload); err != nil {
			slog.Error("ghtk webhook: bind payload", slog.Any("error", err))
			return ec.NoContent(http.StatusBadRequest)
		}

		if payload.LabelID == "" {
			slog.Error("ghtk webhook: missing label_id")
			return ec.NoContent(http.StatusBadRequest)
		}

		status := mapGHTKStatus(payload.StatusID)
		if status == "" {
			slog.Warn("ghtk webhook: unrecognized status_id", slog.Int("status_id", payload.StatusID), slog.String("label_id", payload.LabelID))
			// Return 200 so GHTK does not retry unknown statuses
			return ec.NoContent(http.StatusOK)
		}

		data := map[string]any{
			"label_id":    payload.LabelID,
			"status_id":   payload.StatusID,
			"status_name": payload.StatusName,
			"partner_id":  payload.PartnerID,
			"order_id":    payload.OrderID,
		}

		result := transport.WebhookResult{
			TransportID: payload.LabelID,
			Status:      status,
			Data:        data,
		}

		if err := deliver(ec.Request().Context(), result); err != nil {
			slog.Error("ghtk webhook: deliver error", slog.Any("error", err))
		}

		return ec.NoContent(http.StatusOK)
	})
	return key
}

// extractWeight pulls WeightGrams from the PackageDetails of the first item, defaulting to 500g.
func (g *GHTKClient) extractWeight(items []transport.ItemMetadata) int32 {
	if len(items) == 0 {
		return 500
	}
	var pkg packageDetails
	if err := json.Unmarshal(items[0].PackageDetails, &pkg); err != nil || pkg.WeightGrams == 0 {
		return 500
	}
	return pkg.WeightGrams
}

// calculateShippingCost calculates shipping cost based on weight and service type.
func (g *GHTKClient) calculateShippingCost(weightGrams int32) int64 {
	baseCost := int64(15000) // 15,000 VND base cost

	var weightCost int64
	if weightGrams > 1000 {
		weightCost = (int64(weightGrams) - 1000) / 1000 * 2000 // 2,000 VND per additional kg
	}

	var serviceMultiplier float64
	switch g.data.Method {
	case ServiceExpress:
		serviceMultiplier = 1.5
	case ServiceStandard:
		serviceMultiplier = 1.0
	case ServiceEconomy:
		serviceMultiplier = 0.8
	default:
		serviceMultiplier = 1.0
	}

	totalCost := int64(float64(baseCost+weightCost) * serviceMultiplier)
	return totalCost
}

// calculateETA calculates estimated time of arrival.
func (g *GHTKClient) calculateETA() time.Time {
	now := time.Now()
	switch g.data.Method {
	case ServiceExpress:
		return now.Add(24 * time.Hour)
	case ServiceStandard:
		return now.Add(48 * time.Hour)
	case ServiceEconomy:
		return now.Add(72 * time.Hour)
	default:
		return now.Add(48 * time.Hour)
	}
}

// updateStatus simulates status progression.
func (g *GHTKClient) updateStatus(ship *fakeTransport) {
	now := time.Now()
	elapsed := now.Sub(ship.CreatedAt)

	switch {
	case elapsed < 30*time.Minute:
		ship.Status = "LabelCreated"
	case elapsed < 2*time.Hour:
		ship.Status = "InTransit"
	case elapsed < 24*time.Hour:
		ship.Status = "OutForDelivery"
	case elapsed > 24*time.Hour && ship.Status != "Delivered":
		ship.Status = "Delivered"
	}

	ship.UpdatedAt = now
}

// getCurrentLocation returns a fake location based on transport status.
func (g *GHTKClient) getCurrentLocation(ship *fakeTransport) string {
	switch ship.Status {
	case "LabelCreated":
		return "Hà Nội - Đang chuẩn bị hàng"
	case "InTransit":
		return "Trung tâm phân phối - Đang vận chuyển"
	case "OutForDelivery":
		return "Đang giao hàng - Gần địa chỉ nhận"
	case "Delivered":
		return "Đã giao hàng thành công"
	case "Failed":
		return "Giao hàng thất bại"
	case "Cancelled":
		return "Đơn hàng đã hủy"
	default:
		return "Đang xử lý"
	}
}

// generateTrackingID generates a fake GHTK tracking ID.
func (g *GHTKClient) generateTrackingID() string {
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)
	return fmt.Sprintf("GTK%s", strings.ToUpper(randomHex))
}

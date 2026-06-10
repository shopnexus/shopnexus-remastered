package commonecho

import (
	"net/http"

	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/response"

	"github.com/labstack/echo/v4"
)

type ReverseGeocodeRequest struct {
	Latitude  float64 `json:"latitude"  validate:"required"`
	Longitude float64 `json:"longitude" validate:"required"`
}

// ReverseGeocode converts lat/lng coordinates to an address string.
func (h *Handler) ReverseGeocode(c echo.Context) error {
	var req ReverseGeocodeRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ReverseGeocode(c.Request().Context(), commonbiz.ReverseGeocodeParams{
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ForwardGeocodeRequest struct {
	Address string `json:"address" validate:"required"`
}

// ForwardGeocode converts an address string to lat/lng coordinates.
func (h *Handler) ForwardGeocode(c echo.Context) error {
	var req ForwardGeocodeRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ForwardGeocode(c.Request().Context(), commonbiz.ForwardGeocodeParams{
		Address: req.Address,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type SearchGeocodeRequest struct {
	Query string `query:"q"     validate:"required,min=2"`
	Limit int    `query:"limit"`
}

// SearchGeocode returns location suggestions matching a partial query.
func (h *Handler) SearchGeocode(c echo.Context) error {
	var req SearchGeocodeRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	results, err := h.biz.SearchGeocode(c.Request().Context(), commonbiz.SearchGeocodeParams{
		Query: req.Query,
		Limit: req.Limit,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, results)
}

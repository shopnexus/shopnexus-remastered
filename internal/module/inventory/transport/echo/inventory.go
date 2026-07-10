package inventoryecho

import (
	"net/http"

	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

// Handler handles HTTP requests for the inventory module.
type Handler struct {
	biz inventorybiz.InventoryBizClient
}

// NewHandler registers inventory module routes and returns the handler.
func NewHandler(e *echo.Echo, biz inventorybiz.InventoryBizClient) *Handler {
	h := &Handler{biz: biz}
	api := e.Group("/api/v1/inventory")

	stockApi := api.Group("/stock")
	stockApi.GET("", h.GetStock)
	stockApi.PATCH("", h.UpdateStockSettings)
	stockApi.GET("/history", h.ListStockHistory)
	stockApi.POST("/import", h.ImportStock)

	serialApi := api.Group("/serial")
	serialApi.GET("", h.ListSerial)
	serialApi.PATCH("", h.UpdateSerial)
	return h
}

type GetStockRequest struct {
	RefID   uuid.UUID                         `query:"ref_id"   validate:"required"`
	RefType inventorydb.InventoryStockRefType `query:"ref_type" validate:"required,validateFn=Valid"`
}

// GetStock returns the stock record for a given reference.
//
//	@Summary	Get stock
//	@Tags		inventory
//	@Produce	json
//	@Security	BearerAuth
//	@Param		ref_id		query		string	true	"Reference ID (UUID)"
//	@Param		ref_type	query		string	true	"Reference type"
//	@Success	200			{object}	response.CommonResponse{data=inventorydb.InventoryStock}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/inventory/stock [get]
func (h *Handler) GetStock(c echo.Context) error {
	var req GetStockRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.GetStock(c.Request().Context(), inventorybiz.GetStockParams{
		RefID:   req.RefID,
		RefType: req.RefType,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListStockHistoryRequest struct {
	paginate.Params

	RefID   uuid.UUID                         `query:"ref_id"   validate:"required"`
	RefType inventorydb.InventoryStockRefType `query:"ref_type" validate:"required,validateFn=Valid"`
}

// ListStockHistory returns the paginated stock change history for a reference.
//
//	@Summary	List stock history
//	@Tags		inventory
//	@Produce	json
//	@Security	BearerAuth
//	@Param		ref_id		query		string	true	"Reference ID (UUID)"
//	@Param		ref_type	query		string	true	"Reference type"
//	@Param		page		query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit		query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor		query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort		query		string	false	"Sort, e.g. -date_created"
//	@Success	200			{object}	response.SwaggerPaginationResponse{data=[]inventorydb.InventoryStockHistory}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/inventory/stock/history [get]
func (h *Handler) ListStockHistory(c echo.Context) error {
	var req ListStockHistoryRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListStockHistory(c.Request().Context(), inventorybiz.ListStockHistoryParams{
		Params:  req.Params.Constrain(),
		RefID:   req.RefID,
		RefType: req.RefType,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type UpdateStockSettingsRequest struct {
	RefID          uuid.UUID                         `json:"ref_id"          validate:"required"`
	RefType        inventorydb.InventoryStockRefType `json:"ref_type"        validate:"required,validateFn=Valid"`
	SerialRequired null.Bool                         `json:"serial_required" validate:"omitnil"`
}

// UpdateStockSettings updates settings (e.g. serial requirement) for a stock.
//
//	@Summary	Update stock settings
//	@Tags		inventory
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		UpdateStockSettingsRequest	true	"Settings to update"
//	@Success	200		{object}	response.CommonResponse{data=inventorydb.InventoryStock}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/inventory/stock [patch]
func (h *Handler) UpdateStockSettings(c echo.Context) error {
	var req UpdateStockSettingsRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Call().UpdateStockSettings(c.Request().Context(), inventorybiz.UpdateStockSettingsParams{
		RefID:          req.RefID,
		RefType:        req.RefType,
		SerialRequired: req.SerialRequired,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ImportStockRequest struct {
	RefID     uuid.UUID                         `json:"ref_id"     validate:"required"`
	RefType   inventorydb.InventoryStockRefType `json:"ref_type"   validate:"required,validateFn=Valid"`
	Change    int64                             `json:"change"     validate:"required,gt=0"`
	SerialIDs []string                          `json:"serial_ids" validate:"dive,required"`
}

// ImportStock adds stock (and optional serials) for a reference.
//
//	@Summary	Import stock
//	@Tags		inventory
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		ImportStockRequest	true	"Stock to import"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/inventory/stock/import [post]
func (h *Handler) ImportStock(c echo.Context) error {
	var req ImportStockRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	if err := h.biz.Call().ImportStock(c.Request().Context(), inventorybiz.ImportStockParams{
		RefID:     req.RefID,
		RefType:   req.RefType,
		Change:    req.Change,
		SerialIDs: req.SerialIDs,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "add stock successfully")
}

type ListProductSerialRequest struct {
	paginate.Params

	StockID int64 `query:"stock_id" validate:"required,gt=0"`
}

// ListSerial returns the paginated serials for a stock.
//
//	@Summary	List serials
//	@Tags		inventory
//	@Produce	json
//	@Security	BearerAuth
//	@Param		stock_id	query		int		true	"Stock ID"
//	@Param		page		query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit		query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor		query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort		query		string	false	"Sort, e.g. -date_created"
//	@Success	200			{object}	response.SwaggerPaginationResponse{data=[]inventorydb.InventorySerial}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/inventory/serial [get]
func (h *Handler) ListSerial(c echo.Context) error {
	var req ListProductSerialRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListSerial(c.Request().Context(), inventorybiz.ListSerialParams{
		Params:  req.Params.Constrain(),
		StockID: req.StockID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type UpdateSerialRequest struct {
	SerialIDs []string                    `json:"serial_ids" validate:"required,dive,required"`
	Status    inventorydb.InventoryStatus `json:"status"     validate:"required,validateFn=Valid"`
}

// UpdateSerial updates the status of one or more serials.
//
//	@Summary	Update serials
//	@Tags		inventory
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		UpdateSerialRequest	true	"Serials and target status"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/inventory/serial [patch]
func (h *Handler) UpdateSerial(c echo.Context) error {
	var req UpdateSerialRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	if err := h.biz.Call().UpdateSerial(c.Request().Context(), inventorybiz.UpdateSerialParams{
		SerialIDs: req.SerialIDs,
		Status:    req.Status,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "update sku serial successfully")
}

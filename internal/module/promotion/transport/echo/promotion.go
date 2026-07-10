package catalogecho

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
	"github.com/samber/lo"

	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	promotionmodel "shopnexus-server/internal/module/promotion/model"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"
)

// Handler handles HTTP requests for the promotion module.
type Handler struct {
	biz promotionbiz.PromotionBizClient
}

// NewHandler registers promotion module routes and returns the handler.
func NewHandler(e *echo.Echo, biz promotionbiz.PromotionBizClient) *Handler {
	h := &Handler{biz: biz}

	api := e.Group("/api/v1/catalog/promotion")
	api.GET("/:id", h.GetPromotion)
	api.GET("", h.ListPromotion)
	api.POST("", h.CreatePromotion)
	api.PATCH("", h.UpdatePromotion)
	api.DELETE("/:id", h.DeletePromotion)

	return h
}

// --- Shared types ---

type PromotionRefRequest struct {
	RefType promotionmodel.RefType `json:"ref_type" validate:"required"`
	RefID   uuid.UUID              `json:"ref_id"   validate:"required"`
}

func mapRefs(reqs []PromotionRefRequest) []promotionmodel.PromotionRef {
	return lo.Map(reqs, func(r PromotionRefRequest, _ int) promotionmodel.PromotionRef {
		return promotionmodel.PromotionRef{
			RefType: r.RefType,
			RefID:   r.RefID,
		}
	})
}

// --- Get ---

type GetPromotionRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

// GetPromotion returns a single promotion by ID.
//
//	@Summary	Get promotion
//	@Tags		promotion
//	@Produce	json
//	@Param		id	path		string	true	"Promotion ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=promotionmodel.Promotion}
//	@Failure	400	{object}	response.CommonResponse
//	@Router		/catalog/promotion/{id} [get]
func (h *Handler) GetPromotion(c echo.Context) error {
	var req GetPromotionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.GetPromotion(c.Request().Context(), promotionbiz.GetPromotionParams{
		ID: req.ID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- List ---

type ListPromotionRequest struct {
	paginate.Params
}

// ListPromotion returns paginated promotions.
//
//	@Summary	List promotions
//	@Tags		promotion
//	@Produce	json
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]promotionmodel.Promotion}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/catalog/promotion [get]
func (h *Handler) ListPromotion(c echo.Context) error {
	var req ListPromotionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListPromotion(c.Request().Context(), promotionbiz.ListPromotionParams{
		Params: req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

// --- Create ---

type CreatePromotionRequest struct {
	Code        string                `json:"code"         validate:"required"`
	Type        promotionmodel.Type   `json:"type"         validate:"required"`
	Title       string                `json:"title"        validate:"required"`
	Description null.String           `json:"description"  validate:"omitnil"`
	IsEnabled   bool                  `json:"is_enabled"`
	AutoApply   bool                  `json:"auto_apply"`
	Group       string                `json:"group"        validate:"required"`
	Priority    int32                 `json:"priority"`
	Data        json.RawMessage       `json:"data"`
	DateStarted time.Time             `json:"date_started" validate:"required"`
	DateEnded   null.Time             `json:"date_ended"   validate:"omitnil"`
	Refs        []PromotionRefRequest `json:"refs"         validate:"dive"`
}

// CreatePromotion creates a new promotion.
//
//	@Summary	Create promotion
//	@Tags		promotion
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreatePromotionRequest	true	"Promotion payload"
//	@Success	200		{object}	response.CommonResponse{data=promotionmodel.Promotion}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/catalog/promotion [post]
func (h *Handler) CreatePromotion(c echo.Context) error {
	var req CreatePromotionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.biz.Call().CreatePromotion(c.Request().Context(), promotionbiz.CreatePromotionParams{
		Account:     claims.Account,
		Code:        req.Code,
		Type:        req.Type,
		Title:       req.Title,
		Description: req.Description,
		IsEnabled:   req.IsEnabled,
		AutoApply:   req.AutoApply,
		Group:       req.Group,
		Priority:    req.Priority,
		Data:        req.Data,
		DateStarted: req.DateStarted,
		DateEnded:   req.DateEnded,
		Refs:        mapRefs(req.Refs),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Update ---

type UpdatePromotionRequest struct {
	ID            uuid.UUID              `json:"id"              validate:"required"`
	Code          null.String            `json:"code"            validate:"omitnil"`
	OwnerID       uuid.NullUUID          `json:"owner_id"        validate:"omitnil"`
	NullOwnerID   bool                   `json:"null_owner_id"`
	Title         null.String            `json:"title"           validate:"omitnil"`
	Description   null.String            `json:"description"     validate:"omitnil"`
	IsEnabled     null.Bool              `json:"is_enabled"      validate:"omitnil"`
	AutoApply     null.Bool              `json:"auto_apply"      validate:"omitnil"`
	Group         null.String            `json:"group"           validate:"omitnil"`
	Priority      null.Int32             `json:"priority"        validate:"omitnil"`
	Data          json.RawMessage        `json:"data"`
	NullData      bool                   `json:"null_data"`
	DateStarted   null.Time              `json:"date_started"    validate:"omitnil"`
	DateEnded     null.Time              `json:"date_ended"      validate:"omitnil"`
	NullDateEnded bool                   `json:"null_date_ended"`
	Refs          *[]PromotionRefRequest `json:"refs"`
}

// UpdatePromotion patches an existing promotion.
//
//	@Summary	Update promotion
//	@Tags		promotion
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		UpdatePromotionRequest	true	"Fields to update"
//	@Success	200		{object}	response.CommonResponse{data=promotionmodel.Promotion}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/catalog/promotion [patch]
func (h *Handler) UpdatePromotion(c echo.Context) error {
	var req UpdatePromotionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	params := promotionbiz.UpdatePromotionParams{
		Account:       claims.Account,
		ID:            req.ID,
		Code:          req.Code,
		OwnerID:       req.OwnerID,
		NullOwnerID:   req.NullOwnerID,
		Title:         req.Title,
		Description:   req.Description,
		IsEnabled:     req.IsEnabled,
		AutoApply:     req.AutoApply,
		Group:         req.Group,
		Priority:      req.Priority,
		Data:          req.Data,
		NullData:      req.NullData,
		DateStarted:   req.DateStarted,
		DateEnded:     req.DateEnded,
		NullDateEnded: req.NullDateEnded,
	}

	if req.Refs != nil {
		refs := mapRefs(*req.Refs)
		params.Refs = &refs
	}

	result, err := h.biz.Call().UpdatePromotion(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Delete ---

type DeletePromotionRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

// DeletePromotion removes a promotion by ID.
//
//	@Summary	Delete promotion
//	@Tags		promotion
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Promotion ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=string}
//	@Failure	400	{object}	response.CommonResponse
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/catalog/promotion/{id} [delete]
func (h *Handler) DeletePromotion(c echo.Context) error {
	var req DeletePromotionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err = h.biz.Call().DeletePromotion(c.Request().Context(), promotionbiz.DeletePromotionParams{
		Account: claims.Account,
		ID:      req.ID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Promotion deleted successfully")
}

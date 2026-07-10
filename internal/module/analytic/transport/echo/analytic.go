package accountecho

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/samber/lo"

	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	orderbiz "shopnexus-server/internal/module/order/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"
)

// Handler handles HTTP requests for the analytic module.
type Handler struct {
	biz analyticbiz.AnalyticBizClient
	// order backs the seller dashboard: the composed view is owned by the
	// order module, which reads its own stats locally.
	order orderbiz.OrderBizClient
}

// NewHandler registers analytic module routes and returns the handler.
func NewHandler(e *echo.Echo, biz analyticbiz.AnalyticBizClient, order orderbiz.OrderBizClient) *Handler {
	h := &Handler{biz: biz, order: order}
	api := e.Group("/api/v1/analytic")
	api.POST("/interaction", h.CreateInteraction)
	api.GET("/popularity/top", h.ListTopProductPopularity)
	api.GET("/popularity/:spu_id", h.GetProductPopularity)
	api.GET("/seller-dashboard", h.GetSellerDashboard)

	return h
}

type CreateInteraction struct {
	EventType string                           `json:"event_type" validate:"required,min=1"`
	RefType   analyticmodel.InteractionRefType `json:"ref_type"   validate:"required,validateFn=Valid"`
	RefID     string                           `json:"ref_id"     validate:"required"`
}

type CreateInteractionRequest struct {
	Interactions []CreateInteraction `json:"interactions" validate:"required,dive,required"`
}

// CreateInteraction records a batch of user interaction events.
//
//	@Summary	Create interactions
//	@Tags		analytic
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateInteractionRequest	true	"Interactions to record"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/analytic/interaction [post]
func (h *Handler) CreateInteraction(c echo.Context) error {
	var req CreateInteractionRequest
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

	if err := h.biz.Call().CreateInteraction(c.Request().Context(), analyticbiz.CreateInteractionParams{
		Interactions: lo.Map(req.Interactions, func(i CreateInteraction, _ int) analyticbiz.CreateInteraction {
			return analyticbiz.CreateInteraction{
				Account:   claims.Account,
				EventType: analyticmodel.Event(i.EventType),
				RefType:   i.RefType,
				RefID:     i.RefID,
			}
		}),
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Interaction created successfully")
}

type GetProductPopularityRequest struct {
	SpuID uuid.UUID `param:"spu_id" validate:"required"`
}

// GetProductPopularity returns the popularity record for a product (SPU).
//
//	@Summary	Get product popularity
//	@Tags		analytic
//	@Produce	json
//	@Param		spu_id	path		string	true	"SPU ID (UUID)"
//	@Success	200		{object}	response.CommonResponse{data=analyticdb.AnalyticProductPopularity}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/analytic/popularity/{spu_id} [get]
func (h *Handler) GetProductPopularity(c echo.Context) error {
	var req GetProductPopularityRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.GetProductPopularity(c.Request().Context(), req.SpuID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListTopProductPopularityRequest struct {
	paginate.Params
}

// ListTopProductPopularity returns the top products ranked by popularity.
//
//	@Summary	List top product popularity
//	@Tags		analytic
//	@Produce	json
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.CommonResponse{data=[]analyticdb.AnalyticProductPopularity}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/analytic/popularity/top [get]
func (h *Handler) ListTopProductPopularity(c echo.Context) error {
	var req ListTopProductPopularityRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListTopProductPopularity(c.Request().Context(), req.Params.Constrain())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type GetSellerDashboardRequest struct {
	Start       string `query:"start"`
	End         string `query:"end"`
	Granularity string `query:"granularity"`
}

// GetSellerDashboard returns aggregated dashboard stats for the seller.
//
//	@Summary	Get seller dashboard
//	@Tags		analytic
//	@Produce	json
//	@Security	BearerAuth
//	@Param		start		query		string	false	"Start date (RFC3339)"
//	@Param		end			query		string	false	"End date (RFC3339)"
//	@Param		granularity	query		string	false	"Aggregation granularity"
//	@Success	200			{object}	response.CommonResponse{data=dashboard.SellerDashboard}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/analytic/seller-dashboard [get]
func (h *Handler) GetSellerDashboard(c echo.Context) error {
	var req GetSellerDashboardRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	params := orderbiz.GetSellerDashboardParams{
		SellerID:    claims.Account.ID,
		Granularity: req.Granularity,
	}

	if req.Start != "" {
		t, err := time.Parse(time.RFC3339, req.Start)
		if err != nil {
			return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
		}
		params.StartDate = t
	}
	if req.End != "" {
		t, err := time.Parse(time.RFC3339, req.End)
		if err != nil {
			return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
		}
		params.EndDate = t
	}

	result, err := h.order.GetSellerDashboard(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

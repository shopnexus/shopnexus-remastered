package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	orderbiz "shopnexus-server/internal/module/order/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type ListCommentRequest struct {
	paginate.Params // page | cursor | sort | limit

	RefType   catalogdb.CatalogCommentRefType `query:"ref_type"   validate:"required"`
	RefID     uuid.UUID                       `query:"ref_id"     validate:"required"`
	ID        []uuid.UUID                     `query:"id"         validate:"omitempty"`
	AccountID []uuid.UUID                     `query:"account_id" validate:"omitempty"`
	ScoreFrom null.Float                      `query:"score_from" validate:"omitnil"`
	ScoreTo   null.Float                      `query:"score_to"   validate:"omitnil"`
}

func (h *Handler) ListComment(c echo.Context) error {
	var req ListCommentRequest
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

	result, err := h.biz.ListComment(c.Request().Context(), catalogbiz.ListCommentParams{
		Params:    req.Params.Constrain(),
		Account:   claims.Account,
		RefType:   req.RefType,
		ID:        req.ID,
		AccountID: req.AccountID,
		RefID:     []uuid.UUID{req.RefID},
		ScoreFrom: req.ScoreFrom,
		ScoreTo:   req.ScoreTo,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

type CreateCommentRequest struct {
	RefType     catalogdb.CatalogCommentRefType `json:"ref_type"     validate:"required,validateFn=Valid"`
	RefID       uuid.UUID                       `json:"ref_id"       validate:"required"`
	Body        string                          `json:"body"         validate:"required"`
	Score       float64                         `json:"score"        validate:"required"`
	OrderID     uuid.UUID                       `json:"order_id"     validate:"required"`
	ResourceIDs []uuid.UUID                     `json:"resource_ids" validate:"required"`
}

func (h *Handler) CreateComment(c echo.Context) error {
	var req CreateCommentRequest
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

	// Product reviews go through the order module, which owns purchase
	// validation; replies and other comments hit catalog directly.
	if req.RefType == catalogdb.CatalogCommentRefTypeProductSpu {
		result, err := h.order.CreateProductReview(c.Request().Context(), orderbiz.CreateProductReviewParams{
			Account:     claims.Account,
			SpuID:       req.RefID,
			OrderID:     req.OrderID,
			Body:        req.Body,
			Score:       req.Score,
			ResourceIDs: req.ResourceIDs,
		})
		if err != nil {
			return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
		}
		return response.FromDTO(c.Response().Writer, http.StatusOK, result)
	}

	result, err := h.biz.CreateComment(c.Request().Context(), catalogbiz.CreateCommentParams{
		Account:     claims.Account,
		RefType:     req.RefType,
		RefID:       req.RefID,
		Body:        req.Body,
		Score:       req.Score,
		OrderID:     req.OrderID,
		ResourceIDs: req.ResourceIDs,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type UpdateCommentRequest struct {
	ID             uuid.UUID   `json:"id"              validate:"required"`
	Body           null.String `json:"body"            validate:"required"`
	Score          null.Float  `json:"score"           validate:"required"`
	ResourceIDs    []uuid.UUID `json:"resource_ids"    validate:"required"`
	EmptyResources bool        `json:"empty_resources" validate:"omitempty"`
}

func (h *Handler) UpdateComment(c echo.Context) error {
	var req UpdateCommentRequest
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

	comment, err := h.biz.UpdateComment(c.Request().Context(), catalogbiz.UpdateCommentParams{
		Account:     claims.Account,
		ID:          req.ID,
		Body:        req.Body,
		Score:       req.Score,
		ResourceIDs: req.ResourceIDs,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, comment)
}

type VoteCommentRequest struct {
	CommentID uuid.UUID `json:"comment_id" validate:"required"`
	Vote      string    `json:"vote"       validate:"required,oneof=upvote downvote"`
}

func (h *Handler) VoteComment(c echo.Context) error {
	var req VoteCommentRequest
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

	params := catalogbiz.UpdateCommentParams{
		Account: claims.Account,
		ID:      req.CommentID,
	}
	if req.Vote == "upvote" {
		params.UpvoteDelta = null.IntFrom(1)
	} else {
		params.DownvoteDelta = null.IntFrom(1)
	}

	comment, err := h.biz.UpdateComment(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, comment)
}

type DeleteCommentRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required"`
}

func (h *Handler) DeleteComment(c echo.Context) error {
	var req DeleteCommentRequest
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

	if err := h.biz.DeleteComment(c.Request().Context(), catalogbiz.DeleteCommentParams{
		Account:    claims.Account,
		CommentIDs: req.IDs,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "comment deleted")
}

type ListReviewableOrdersRequest struct {
	SpuID uuid.UUID `query:"spu_id" validate:"required"`
}

func (h *Handler) ListReviewableOrders(c echo.Context) error {
	var req ListReviewableOrdersRequest
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

	result, err := h.order.ListReviewableOrdersBySpu(c.Request().Context(), orderbiz.ListReviewableOrdersBySpuParams{
		Account: claims.Account,
		SpuID:   req.SpuID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

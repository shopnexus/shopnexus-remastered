package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type ListProductSpuRequest struct {
	paginate.Params

	Search     null.String `query:"search"      validate:"omitnil"`
	Slug       []string    `query:"slug"        validate:"omitempty" comma_separated:"true"`
	MyProducts bool        `query:"my_products" validate:"omitempty"`
	CategoryID []uuid.UUID `query:"category_id" validate:"omitempty" comma_separated:"true"`
	IsEnabled  []bool      `query:"is_enabled"   validate:"omitempty" comma_separated:"true"`
}

func (h *Handler) ListProductSpu(c echo.Context) error {
	var req ListProductSpuRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, _ := authclaims.GetClaims(c.Request())

	// Filter by authenticated account when my_products=true
	var accountID []uuid.UUID
	if req.MyProducts && claims.Account.ID != uuid.Nil {
		accountID = []uuid.UUID{claims.Account.ID}
	}

	result, err := h.biz.ListProductSpu(c.Request().Context(), catalogbiz.ListProductSpuParams{
		Params:     req.Params.Constrain(),
		Account:    claims.Account,
		Search:     req.Search,
		Slug:       req.Slug,
		AccountID:  accountID,
		CategoryID: req.CategoryID,
		IsEnabled:  req.IsEnabled,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

type GetProductSpuRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

func (h *Handler) GetProductSpu(c echo.Context) error {
	var req GetProductSpuRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProductSpu(c.Request().Context(), catalogbiz.ListProductSpuParams{
		ID: []uuid.UUID{req.ID},
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result.Data[0])
}

type CreateProductSpuRequest struct {
	CategoryID     uuid.UUID                           `json:"category_id"    validate:"required"`
	Name           string                              `json:"name"           validate:"required,min=1,max=200"`
	Description    string                              `json:"description"    validate:"required,max=1000000"`
	Currency       string                              `json:"currency"       validate:"required,iso4217"`
	IsEnabled      bool                                `json:"is_enabled"      validate:"omitempty"`
	Tags           []string                            `json:"tags"           validate:"required,dive,min=1,max=100"`
	ResourceIDs    []uuid.UUID                         `json:"resource_ids"   validate:"omitempty,dive"`
	Specifications []catalogmodel.ProductSpecification `json:"specifications" validate:"omitempty,dive"`
}

func (h *Handler) CreateProductSpu(c echo.Context) error {
	var req CreateProductSpuRequest
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

	spu, err := h.biz.CreateProductSpu(c.Request().Context(), catalogbiz.CreateProductSpuParams{
		Account:        claims.Account,
		CategoryID:     req.CategoryID,
		Name:           req.Name,
		Description:    req.Description,
		Currency:       req.Currency,
		IsEnabled:      req.IsEnabled,
		Tags:           req.Tags,
		ResourceIDs:    req.ResourceIDs,
		Specifications: req.Specifications,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, spu)
}

type UpdateProductSpuRequest struct {
	ID             uuid.UUID                           `json:"id"              validate:"required"`
	CategoryID     uuid.NullUUID                       `json:"category_id"     validate:"omitnil"`
	FeaturedSkuID  uuid.NullUUID                       `json:"featured_sku_id" validate:"omitnil"`
	Name           null.String                         `json:"name"            validate:"omitnil,min=1,max=200"`
	Description    null.String                         `json:"description"     validate:"omitnil,max=100000"`
	Currency       null.String                         `json:"currency"        validate:"omitnil,iso4217"`
	IsEnabled      null.Bool                           `json:"is_enabled"      validate:"omitnil"`
	RegenerateSlug bool                                `json:"regenerate_slug"`
	Tags           []string                            `json:"tags"            validate:"omitempty,dive,min=1,max=100"`
	ResourceIDs    []uuid.UUID                         `json:"resource_ids"    validate:"omitempty,dive"`
	Specifications []catalogmodel.ProductSpecification `json:"specifications"  validate:"omitempty,dive"`
}

func (h *Handler) UpdateProductSpu(c echo.Context) error {
	var req UpdateProductSpuRequest
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

	spu, err := h.biz.UpdateProductSpu(c.Request().Context(), catalogbiz.UpdateProductSpuParams{
		Account:        claims.Account,
		ID:             req.ID,
		FeaturedSkuID:  req.FeaturedSkuID,
		CategoryID:     req.CategoryID,
		Name:           req.Name,
		Description:    req.Description,
		Currency:       req.Currency,
		IsEnabled:      req.IsEnabled,
		RegenerateSlug: req.RegenerateSlug,
		Tags:           req.Tags,
		ResourceIDs:    req.ResourceIDs,
		Specifications: req.Specifications,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, spu)
}

type DeleteProductSpuRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

func (h *Handler) DeleteProductSpu(c echo.Context) error {
	var req DeleteProductSpuRequest
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

	if err := h.biz.DeleteProductSpu(c.Request().Context(), catalogbiz.DeleteProductSpuParams{
		Account: claims.Account,
		ID:      req.ID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "deleted")
}

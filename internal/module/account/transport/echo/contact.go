package accountecho

import (
	"net/http"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

// ListContact returns all contacts belonging to the authenticated caller.
//
//	@Summary	List contacts
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	response.CommonResponse{data=[]accountdb.AccountContact}
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/account/contact [get]
func (h *Handler) ListContact(c echo.Context) error {
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.biz.ListContact(c.Request().Context(), accountbiz.ListContactParams{
		AccountID: []uuid.UUID{claims.Account.ID},
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type GetContactRequest struct {
	ContactID uuid.UUID `param:"contact_id" validate:"required"`
}

// GetContact returns a single contact by ID.
//
//	@Summary	Get contact
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Param		contact_id	path		string	true	"Contact ID (UUID)"
//	@Success	200			{object}	response.CommonResponse{data=accountdb.AccountContact}
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/account/contact/{contact_id} [get]
func (h *Handler) GetContact(c echo.Context) error {
	var req GetContactRequest
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

	result, err := h.biz.GetContact(c.Request().Context(), accountbiz.GetContactParams{
		Account:   claims.Account,
		ContactID: req.ContactID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type CreateContactRequest struct {
	FullName      string                   `json:"full_name"      validate:"required"`
	Phone         string                   `json:"phone"          validate:"required"`
	Address       string                   `json:"address"        validate:"required"`
	AddressDetail null.String              `json:"address_detail" validate:"omitnil"`
	AddressType   accountmodel.AddressType `json:"address_type"   validate:"required,validateFn=Valid"`
	Latitude      float64                  `json:"latitude"       validate:"latitude"`
	Longitude     float64                  `json:"longitude"      validate:"longitude"`
}

// CreateContact adds a new contact/address for the caller.
//
//	@Summary	Create contact
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateContactRequest	true	"Contact payload"
//	@Success	200		{object}	response.CommonResponse{data=accountdb.AccountContact}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/contact [post]
func (h *Handler) CreateContact(c echo.Context) error {
	var req CreateContactRequest
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

	result, err := h.biz.Call().CreateContact(c.Request().Context(), accountbiz.CreateContactParams{
		Account:       claims.Account,
		FullName:      req.FullName,
		Phone:         req.Phone,
		Address:       req.Address,
		AddressDetail: req.AddressDetail,
		AddressType:   req.AddressType,
		Latitude:      req.Latitude,
		Longitude:     req.Longitude,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type UpdateContactRequest struct {
	ContactID     uuid.UUID                    `json:"contact_id"     validate:"required"`
	FullName      null.String                  `json:"full_name"      validate:"omitnil"`
	Phone         null.String                  `json:"phone"          validate:"omitnil"`
	Address       null.String                  `json:"address"        validate:"omitnil"`
	AddressDetail null.String                  `json:"address_detail" validate:"omitnil"`
	AddressType   accountmodel.NullAddressType `json:"address_type"   validate:"omitnil,validateFn=Valid"`
	PhoneVerified null.Bool                    `json:"phone_verified" validate:"omitnil"`
	Latitude      null.Float                   `json:"latitude"       validate:"omitnil,latitude"`
	Longitude     null.Float                   `json:"longitude"      validate:"omitnil,longitude"`
}

// UpdateContact patches an existing contact (all fields optional except ID).
//
//	@Summary	Update contact
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		UpdateContactRequest	true	"Fields to update"
//	@Success	200		{object}	response.CommonResponse{data=accountdb.AccountContact}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/contact [patch]
func (h *Handler) UpdateContact(c echo.Context) error {
	var req UpdateContactRequest
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

	result, err := h.biz.Call().UpdateContact(c.Request().Context(), accountbiz.UpdateContactParams{
		Account:       claims.Account,
		ContactID:     req.ContactID,
		FullName:      req.FullName,
		Phone:         req.Phone,
		Address:       req.Address,
		AddressDetail: req.AddressDetail,
		AddressType:   req.AddressType,
		PhoneVerified: req.PhoneVerified,
		Latitude:      req.Latitude,
		Longitude:     req.Longitude,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type DeleteContactRequest struct {
	ContactID uuid.UUID `json:"contact_id" validate:"required"`
}

// DeleteContact removes a contact by ID.
//
//	@Summary	Delete contact
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		DeleteContactRequest	true	"Contact ID"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/contact [delete]
func (h *Handler) DeleteContact(c echo.Context) error {
	var req DeleteContactRequest
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

	if err := h.biz.Call().DeleteContact(c.Request().Context(), accountbiz.DeleteContactParams{
		Account:   claims.Account,
		ContactID: req.ContactID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Delete contact successfully")
}

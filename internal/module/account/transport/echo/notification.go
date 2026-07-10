package accountecho

import (
	"net/http"

	accountbiz "shopnexus-server/internal/module/account/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/labstack/echo/v4"
)

type ListNotificationRequest struct {
	paginate.Params
}

// ListNotification returns the caller's notifications (paginated).
//
//	@Summary	List notifications
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]accountdb.AccountNotification}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/notification [get]
func (h *Handler) ListNotification(c echo.Context) error {
	var req ListNotificationRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.biz.ListNotification(c.Request().Context(), accountbiz.ListNotificationParams{
		Account: claims.Account,
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

// CountUnread returns the caller's unread notification count.
//
//	@Summary	Count unread notifications
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	response.CommonResponse{data=object}
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/account/notification/unread-count [get]
func (h *Handler) CountUnread(c echo.Context) error {
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	count, err := h.biz.CountUnread(c.Request().Context(), accountbiz.CountUnreadParams{
		AccountID: claims.Account.ID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, map[string]int64{"count": count})
}

type MarkReadRequest struct {
	IDs []int64 `json:"ids" validate:"required,min=1"`
}

// MarkRead marks the given notification IDs as read.
//
//	@Summary	Mark notifications read
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		MarkReadRequest	true	"Notification IDs"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/notification/read [post]
func (h *Handler) MarkRead(c echo.Context) error {
	var req MarkReadRequest
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

	if err := h.biz.Call().MarkRead(c.Request().Context(), accountbiz.MarkReadParams{
		Account: claims.Account,
		IDs:     req.IDs,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Notifications marked as read")
}

// MarkAllRead marks all of the caller's notifications as read.
//
//	@Summary	Mark all notifications read
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	response.CommonResponse{data=string}
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/account/notification/read-all [post]
func (h *Handler) MarkAllRead(c echo.Context) error {
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err := h.biz.Call().MarkAllRead(c.Request().Context(), accountbiz.MarkAllReadParams{
		AccountID: claims.Account.ID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "All notifications marked as read")
}

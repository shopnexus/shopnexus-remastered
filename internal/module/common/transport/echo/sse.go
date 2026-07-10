package commonecho

import (
	"net/http"

	authclaims "shopnexus-server/internal/shared/claims"

	"github.com/labstack/echo/v4"
)

// HandleSSE establishes an SSE connection for the authenticated account.
//
//	@Summary	Server-sent events stream
//	@Tags		common
//	@Produce	text/event-stream
//	@Security	BearerAuth
//	@Param		token	query		string	false	"Bearer token fallback for EventSource (which cannot set headers)"
//	@Success	200		{string}	string	"SSE stream"
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/common/stream [get]
func (h *Handler) HandleSSE(c echo.Context) error {
	// Auth: header first, query param fallback (browser EventSource can't set headers)
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		token := c.QueryParam("token")
		if token == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "missing token")
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)
		claims, err = authclaims.GetClaimsByHeader(header)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
		}
	}

	accountID := claims.Account.ID

	// Set SSE headers
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Flush()

	client, ch := h.handler.SubscribeSSE(accountID)
	defer h.handler.UnsubscribeSSE(accountID, client)

	ctx := c.Request().Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := w.Write(msg); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}

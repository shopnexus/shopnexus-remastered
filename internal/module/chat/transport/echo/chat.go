package chatecho

import (
	"encoding/json"
	"net/http"

	chatbiz "shopnexus-server/internal/module/chat/biz"
	chatdb "shopnexus-server/internal/module/chat/db/sqlc"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// Handler handles HTTP requests for the chat module.
type Handler struct {
	biz chatbiz.ChatBiz
}

// NewHandler registers chat module routes and returns the handler.
func NewHandler(e *echo.Echo, biz chatbiz.ChatBiz) *Handler {
	h := &Handler{biz: biz}

	api := e.Group("/api/v1/chat")
	api.POST("/conversation", h.CreateConversation)
	api.GET("/conversation", h.ListConversation)
	api.GET("/conversation/:id/messages", h.ListMessage)
	api.POST("/send-message", h.SendMessage)
	api.POST("/mark-read", h.MarkRead)

	return h
}

type CreateConversationRequest struct {
	SellerID uuid.UUID `json:"seller_id" validate:"required"`
}

func (h *Handler) CreateConversation(c echo.Context) error {
	var req CreateConversationRequest
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

	result, err := h.biz.CreateConversation(c.Request().Context(), chatbiz.CreateConversationParams{
		Account:  claims.Account,
		SellerID: req.SellerID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListConversationRequest struct {
	paginate.Params
}

func (h *Handler) ListConversation(c echo.Context) error {
	var req ListConversationRequest
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

	result, err := h.biz.ListConversation(c.Request().Context(), chatbiz.ListConversationParams{
		Account: claims.Account,
		Params:  req.Params,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type ListMessageRequest struct {
	paginate.Params
	ConversationID uuid.UUID `param:"id" validate:"required"`
}

func (h *Handler) ListMessage(c echo.Context) error {
	var req ListMessageRequest
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

	result, err := h.biz.ListMessage(c.Request().Context(), chatbiz.ListMessageParams{
		Account:        claims.Account,
		ConversationID: req.ConversationID,
		Params:         req.Params,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type SendMessageRequest struct {
	ConversationID uuid.UUID              `json:"conversation_id"    validate:"required"`
	Type           chatdb.ChatMessageType `json:"type"               validate:"required"`
	Content        string                 `json:"content"            validate:"required"`
	Metadata       json.RawMessage        `json:"metadata,omitempty"`
}

func (h *Handler) SendMessage(c echo.Context) error {
	var req SendMessageRequest
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

	msg, err := h.biz.SendMessage(c.Request().Context(), chatbiz.SendMessageParams{
		Account:        claims.Account,
		ConversationID: req.ConversationID,
		Type:           req.Type,
		Content:        req.Content,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, msg)
}

type MarkReadRequest struct {
	ConversationID uuid.UUID `json:"conversation_id" validate:"required"`
}

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

	if err = h.biz.MarkRead(c.Request().Context(), chatbiz.MarkReadParams{
		Account:        claims.Account,
		ConversationID: req.ConversationID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "marked as read")
}

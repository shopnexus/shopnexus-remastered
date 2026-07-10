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
	biz chatbiz.ChatBizClient
}

// NewHandler registers chat module routes and returns the handler.
func NewHandler(e *echo.Echo, biz chatbiz.ChatBizClient) *Handler {
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

// CreateConversation starts a conversation between the caller and a seller.
//
//	@Summary	Create conversation
//	@Tags		chat
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateConversationRequest	true	"Seller to converse with"
//	@Success	200		{object}	response.CommonResponse{data=chatdb.ChatConversation}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/chat/conversation [post]
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

	result, err := h.biz.Call().CreateConversation(c.Request().Context(), chatbiz.CreateConversationParams{
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

// ListConversation returns the caller's paginated conversations.
//
//	@Summary	List conversations
//	@Tags		chat
//	@Produce	json
//	@Security	BearerAuth
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]chatdb.ChatConversation}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/chat/conversation [get]
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

// ListMessage returns the paginated messages of a conversation.
//
//	@Summary	List messages
//	@Tags		chat
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		string	true	"Conversation ID (UUID)"
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]chatdb.ChatMessage}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/chat/conversation/{id}/messages [get]
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
		Params:         req.Params.Constrain(),
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
	Metadata       json.RawMessage        `json:"data,omitempty"`
}

// SendMessage posts a message to a conversation.
//
//	@Summary	Send message
//	@Tags		chat
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		SendMessageRequest	true	"Message payload"
//	@Success	200		{object}	response.CommonResponse{data=chatdb.ChatMessage}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/chat/send-message [post]
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

	msg, err := h.biz.Call().SendMessage(c.Request().Context(), chatbiz.SendMessageParams{
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

// MarkRead marks a conversation as read for the caller.
//
//	@Summary	Mark conversation read
//	@Tags		chat
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		MarkReadRequest	true	"Conversation to mark read"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/chat/mark-read [post]
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

	if err = h.biz.Call().MarkRead(c.Request().Context(), chatbiz.MarkReadParams{
		Account:        claims.Account,
		ConversationID: req.ConversationID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "marked as read")
}

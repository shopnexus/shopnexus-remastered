package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	chatapi "shopnexus/internal/module/chat/api"
)

// Chat serves the chat module's routes: conversations and messages.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Chat struct {
	svc chatapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewChat(svc chatapi.Service, v *validator.Validate, log *slog.Logger) *Chat {
	return &Chat{svc: svc, v: v, log: log}
}

// ListConversations handles GET /conversations.
func (h *Chat) ListConversations(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// OpenConversation handles POST /conversations.
func (h *Chat) OpenConversation(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetUnreadCount handles GET /conversations/unread-count.
func (h *Chat) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetConversation handles GET /conversations/{id}.
func (h *Chat) GetConversation(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListMessages handles GET /conversations/{id}/messages.
func (h *Chat) ListMessages(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// SendMessage handles POST /conversations/{id}/messages.
func (h *Chat) SendMessage(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// MarkConversationRead handles POST /conversations/{id}/read.
func (h *Chat) MarkConversationRead(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateMessage handles PATCH /messages/{id}.
func (h *Chat) UpdateMessage(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RedactMessage handles DELETE /messages/{id}.
func (h *Chat) RedactMessage(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

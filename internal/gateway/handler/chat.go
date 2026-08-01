package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Chat serves the chat module's routes: the thread a pair of accounts shares, its
// messages, and the read marks behind an unread badge.
//
// Cursor-paginated rather than page-paginated, because a thread moves under the reader:
// an offset would skip or repeat a message whenever one arrives mid-read.
type Chat struct {
	svc chatapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewChat(svc chatapi.Service, v *validator.Validate, log *slog.Logger) *Chat {
	return &Chat{svc: svc, v: v, log: log}
}

// CreateUpload handles POST /conversations/uploads — a slot to PUT a message attachment
// into.
func (h *Chat) CreateUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req chatapi.CreateUploadRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ConfirmUpload handles POST /conversations/uploads/{id}/confirmation — the bytes are at
// the store.
func (h *Chat) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	resourceID, err := pathID[id.Resource](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.ConfirmUploadRequest{ActorID: uid, ID: resourceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListConversations handles GET /conversations — the inbox.
func (h *Chat) ListConversations(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.ListConversationsRequest{
		ActorID: uid,
		Cursor:  r.URL.Query().Get("cursor"),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListConversations(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta))
}

// OpenConversation handles POST /conversations. Idempotent: there is one thread per pair,
// so this answers the existing one rather than refusing.
func (h *Chat) OpenConversation(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req chatapi.StartConversationRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.StartConversation(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// GetUnreadCount handles GET /conversations/unread-count — the badge.
func (h *Chat) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.UnreadCountRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetUnreadCount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// GetConversation handles GET /conversations/{id}.
func (h *Chat) GetConversation(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	conversationID, err := pathID[id.Conversation](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.GetConversationRequest{ActorID: uid, ID: conversationID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetConversation(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListMessages handles GET /conversations/{id}/messages — newest first, on a cursor.
func (h *Chat) ListMessages(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	conversationID, err := pathID[id.Conversation](r, "id")
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.ListMessagesRequest{
		ActorID: uid,
		ID:      conversationID,
		Cursor:  r.URL.Query().Get("cursor"),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListMessages(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta))
}

// SendMessage handles POST /conversations/{id}/messages.
func (h *Chat) SendMessage(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	conversationID, err := pathID[id.Conversation](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req chatapi.SendMessageRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ConversationID = uid, conversationID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SendMessage(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// MarkConversationRead handles POST /conversations/{id}/read. The body is optional:
// nothing in it means "everything so far", which is what opening a thread does.
func (h *Chat) MarkConversationRead(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	conversationID, err := pathID[id.Conversation](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req chatapi.MarkConversationReadRequest
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, conversationID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.MarkConversationRead(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateMessage handles PATCH /messages/{id} — the sender's own, and never a system one.
func (h *Chat) UpdateMessage(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	messageID, err := pathID[id.Message](r, "id")
	if failed(w, h.log, err) {
		return
	}
	createdAt, err := timeParam(r, "created_at")
	if failed(w, h.log, err) {
		return
	}
	var req chatapi.UpdateMessageRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID, req.CreatedAt = uid, messageID, createdAt
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateMessage(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// RedactMessage handles DELETE /messages/{id} — unsending. The row stays so the thread
// has no unexplained gaps.
func (h *Chat) RedactMessage(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	messageID, err := pathID[id.Message](r, "id")
	if failed(w, h.log, err) {
		return
	}
	createdAt, err := timeParam(r, "created_at")
	if failed(w, h.log, err) {
		return
	}
	req := chatapi.RedactMessageRequest{ActorID: uid, ID: messageID, CreatedAt: createdAt}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.RedactMessage(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// cursorMeta converts the service's cursor info to the envelope's. NextCursor is a
// pointer there so the last page says null rather than omitting the key.
func cursorMeta(meta chatapi.CursorInfo) httpx.CursorMeta {
	if meta.NextCursor == "" {
		return httpx.CursorMeta{}
	}
	return httpx.CursorMeta{NextCursor: new(meta.NextCursor)}
}

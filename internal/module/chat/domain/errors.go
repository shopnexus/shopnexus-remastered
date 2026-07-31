package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Chat errors. Not-found lives here so the postgres adapter can return it without
// importing the module root.
var (
	ErrConversationNotFound = errx.NewError(http.StatusNotFound, "conversation_not_found", "conversation not found")
	ErrMessageNotFound      = errx.NewError(http.StatusNotFound, "message_not_found", "message not found")
	// ErrNotAParticipant is somebody acting on a thread that is not theirs. The read
	// paths answer not-found instead — a thread they are not in is not theirs to know
	// about — so this is for a write that got past the lookup.
	ErrNotAParticipant  = errx.NewError(http.StatusForbidden, "not_a_participant", "you are not part of this conversation")
	ErrSelfConversation = errx.NewError(http.StatusUnprocessableEntity, "self_conversation", "a conversation needs two different accounts")
	// ErrNotTheSender is editing or redacting somebody else's message. A moderator
	// redacts through the report flow, not through this route.
	ErrNotTheSender = errx.NewError(http.StatusForbidden, "not_the_sender", "only the sender can change this message")
	// ErrMessageRedacted is an edit of something already unsent: the row survives so the
	// thread has no unexplained gaps, but its content is gone for good.
	ErrMessageRedacted = errx.NewError(http.StatusConflict, "message_redacted", "this message was redacted")
	// ErrSystemMessage is a client trying to write or change one. A system message is the
	// backend's word — an offer card, an order update — and a user forging one would be a
	// client asserting something the platform is supposed to attest.
	ErrSystemMessage      = errx.NewError(http.StatusForbidden, "system_message", "a system message is not yours to write or change")
	ErrEmptyMessage       = errx.NewError(http.StatusUnprocessableEntity, "empty_message", "a message needs a body or an attachment")
	ErrAttachmentNotFound = errx.NewError(http.StatusNotFound, "attachment_not_found", "an attachment id names no confirmed resource")
	ErrCursorInvalid      = errx.NewError(http.StatusBadRequest, "cursor_invalid", "the cursor is not one this endpoint issued")
)

package chatmodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the chat module.
var (
	ErrConversationNotFound = errors.NewError(http.StatusNotFound, "conversation_not_found", "The conversation does not exist")
	ErrNotParticipant       = errors.NewError(
		http.StatusForbidden,
		"not_participant",
		"You are not a participant in this conversation",
	)
)

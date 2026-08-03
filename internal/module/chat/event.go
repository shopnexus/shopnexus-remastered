package chat

import (
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/shared/realtime"
)

// The realtime facts chat publishes. Each goes to the conversation's *other* participant:
// the actor already holds the row from their own mutation response, and echoing it back
// would race their optimistic update.
//
// The codes are also the AsyncAPI message names in api/asyncapi.gen.yaml, and
// internal/gateway/asyncapi_contract_test.go fails if the two lists disagree.
var (
	MessageCreated   = realtime.NewEvent[chatapi.Message]("chat.message_created")
	MessageUpdated   = realtime.NewEvent[chatapi.Message]("chat.message_updated")
	MessageDeleted   = realtime.NewEvent[chatapi.DeletedMessageRef]("chat.message_deleted")
	ConversationRead = realtime.NewEvent[chatapi.ConversationReadMark]("chat.conversation_read")
)

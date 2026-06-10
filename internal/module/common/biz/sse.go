package commonbiz

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// SSEEventType identifies the kind of SSE event.
type SSEEventType string

const (
	// Chat events.
	SSENewMessage  SSEEventType = "new_message"
	SSEReadReceipt SSEEventType = "read_receipt"

	// Notification events.
	SSENotification SSEEventType = "notification"
)

// PushEventParams is the payload for PushEvent, called via Restate by other modules.
type PushEventParams struct {
	AccountID uuid.UUID    `json:"account_id"`
	Type      SSEEventType `json:"type"`
	Data      any          `json:"data"`
}

// PushEvent pushes an SSE event to all connected clients of the given account.
// Called via Restate fire-and-forget by other modules.
func (b *CommonHandler) PushEvent(ctx restate.Context, params PushEventParams) error {
	// tail: fan the event out to this node's connected SSE clients.
	return restate.RunVoid(ctx, func(restate.RunContext) error {
		b.pushSSE(params.AccountID, params.Type, params.Data)
		return nil
	})
}

// SubscribeSSE registers a new SSE client for the given account.
func (b *CommonHandler) SubscribeSSE(accountID uuid.UUID) (*SSEClient, <-chan []byte) {
	c := &SSEClient{Ch: make(chan []byte, 64)}

	b.sseMu.Lock()
	b.sseClients[accountID] = append(b.sseClients[accountID], c)
	b.sseMu.Unlock()

	return c, c.Ch
}

// UnsubscribeSSE removes a client and closes its channel.
func (b *CommonHandler) UnsubscribeSSE(accountID uuid.UUID, c *SSEClient) {
	b.sseMu.Lock()
	defer b.sseMu.Unlock()

	clients := b.sseClients[accountID]
	for i, cl := range clients {
		if cl == c {
			b.sseClients[accountID] = append(clients[:i], clients[i+1:]...)
			close(c.Ch)
			break
		}
	}
	if len(b.sseClients[accountID]) == 0 {
		delete(b.sseClients, accountID)
	}
}

// pushSSE sends an SSE-formatted message to all connected clients of the account.
func (b *CommonHandler) pushSSE(accountID uuid.UUID, eventType SSEEventType, data any) {
	payload, err := json.Marshal(map[string]any{
		"type": eventType,
		"data": data,
	})
	if err != nil {
		return
	}
	msg := fmt.Appendf(nil, "data: %s\n\n", payload)

	b.sseMu.RLock()
	clients := b.sseClients[accountID]
	b.sseMu.RUnlock()

	for _, c := range clients {
		select {
		case c.Ch <- msg:
		default:
		}
	}
}

package handler

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/gateway/ws"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/session"
)

// WS serves the realtime socket and the ticket that opens it.
type WS struct {
	hub      *ws.Hub
	tickets  *session.Tickets
	sessions *session.Store
	log      *slog.Logger

	writeTimeout   time.Duration
	pingInterval   time.Duration
	allowedOrigins []string
}

func NewWS(
	hub *ws.Hub,
	tickets *session.Tickets,
	sessions *session.Store,
	log *slog.Logger,
	writeTimeout, pingInterval time.Duration,
	allowedOrigins []string,
) *WS {
	return &WS{
		hub:            hub,
		tickets:        tickets,
		sessions:       sessions,
		log:            log,
		writeTimeout:   writeTimeout,
		pingInterval:   pingInterval,
		allowedOrigins: allowedOrigins,
	}
}

// ticketResponse is deliberately not an id: it is a bearer secret with a TTL.
type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

// CreateTicket handles POST /ws/tickets.
//
// This route carries the Bearer token, so it goes through the normal auth middleware and
// the socket route does not have to: a browser cannot set a header on new WebSocket(), and
// that is the whole reason this endpoint exists.
func (h *WS) CreateTicket(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	sessionID := gwctx.SessionID(r.Context())

	tok, err := h.tickets.Issue(r.Context(), uid.Int64(), sessionID)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, ticketResponse{
		Ticket:    tok,
		ExpiresIn: int(h.tickets.TTL().Seconds()),
	})
}

// Connect handles GET /ws: upgrades to a WebSocket and streams the account's events until
// the client goes away.
//
// It authenticates itself rather than sitting behind middleware.Auth, because the credential
// arrives in the query string as a ticket instead of in a header.
func (h *WS) Connect(w http.ResponseWriter, r *http.Request) {
	accountID, sessionID, err := h.tickets.Redeem(r.Context(), r.URL.Query().Get("ticket"))
	if failed(w, h.log, err) {
		return
	}
	// A ticket issued a moment before a logout is a valid ticket for a dead session.
	// Without this the socket would outlive the revocation that was supposed to end it —
	// exactly the hole middleware.Auth pays a Redis lookup per request to close.
	if _, err := h.sessions.Lookup(r.Context(), sessionID); failed(w, h.log, err) {
		return
	}

	// Join before Accept: refusing with a JSON 429 is more useful to a client than a
	// socket that opens and immediately closes with an opaque code.
	client, err := h.hub.Join(accountID)
	if failed(w, h.log, err) {
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.allowedOrigins,
	})
	if err != nil {
		h.hub.Leave(client)
		// Accept has already written its own response.
		h.log.Warn("websocket accept failed", "account_id", accountID, "err", err)
		return
	}
	defer func() {
		h.hub.Leave(client)
		// CloseNow, not Close: the pump below has already tried a graceful close where one
		// was possible, and this must not block a shutdown.
		if err := conn.CloseNow(); err != nil && !isClosed(err) {
			h.log.Debug("websocket close failed", "account_id", accountID, "err", err)
		}
	}()

	// CloseRead discards anything the client sends and gives a context that is cancelled
	// when it disconnects. The socket is receive-only by design: the client changes state
	// over REST.
	ctx := conn.CloseRead(r.Context())

	h.pump(ctx, conn, client, accountID)
}

// pump writes envelopes and keeps the connection alive.
func (h *WS) pump(ctx context.Context, conn *websocket.Conn, client *ws.Client, accountID int64) {
	ping := time.NewTicker(h.pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-client.Done():
			// The hub dropped us — almost always because this socket fell behind. Say so
			// rather than vanishing, so a client can log it and reconnect knowingly.
			_ = conn.Close(websocket.StatusPolicyViolation, "client too slow")
			return

		case payload, ok := <-client.Out():
			if !ok {
				return
			}
			if err := h.write(ctx, conn, payload); err != nil {
				if !isClosed(err) {
					h.log.Debug("websocket write failed", "account_id", accountID, "err", err)
				}
				return
			}

		case <-ping.C:
			// Ping waits for the pong, so a peer that is gone but has not sent a FIN is
			// discovered here rather than by an accumulating goroutine.
			pingCtx, cancel := context.WithTimeout(ctx, h.writeTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// write sends one envelope under its own deadline. The bytes are already the JSON the
// AsyncAPI document describes, so this writes them rather than re-encoding.
func (h *WS) write(ctx context.Context, conn *websocket.Conn, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, h.writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, payload)
}

// isClosed reports the ordinary end of a connection, which is not worth a log line.
func isClosed(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusAbnormalClosure:
		return true
	}
	return false
}

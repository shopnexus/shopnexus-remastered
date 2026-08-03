// Package ws holds the WebSocket fan-out state for one gateway process: which
// accounts have sockets here, and the subject subscription each one needs.
package ws

import (
	"log/slog"
	"net/http"
	"sync"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/realtime"
)

// ErrTooManySockets refuses a connection past an account's cap. 429 rather than 503:
// it is the caller's own doing and retrying later is the right advice.
var ErrTooManySockets = errx.NewError(http.StatusTooManyRequests, "too_many_sockets", "too many open connections for this account")

// Config is the hub's operational tuning. Every field is required — the values come
// from env config, which has no defaults.
type Config struct {
	// SendBuffer is how many envelopes a socket may fall behind before it is dropped.
	SendBuffer int
	// MaxPerAccount caps concurrent sockets for one account, so a tab-spammer cannot
	// exhaust the process's descriptors.
	MaxPerAccount int
}

// Client is one socket's end of the hub. The handler owns the reading; the hub only
// ever writes to out and closes done.
type Client struct {
	accountID int64
	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

// Out yields envelopes to write to the socket. It is closed when the client is
// dropped, so a write pump can range over it.
func (c *Client) Out() <-chan []byte { return c.out }

// Done is closed when the hub drops this client — because it fell behind, or because
// Leave was called. A write pump selects on it to stop promptly.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		close(c.out)
	})
}

// accountSub is every socket one account has on this replica, plus the fan-out
// subscription feeding them.
type accountSub struct {
	clients map[*Client]struct{}
	cancel  func()
}

// Hub routes fan-out messages to the sockets this process holds.
//
// It subscribes an account's subject when that account's first socket arrives and
// cancels it when the last one leaves, so a replica receives bytes only for accounts
// it is actually serving — the filtering happens in NATS rather than here.
type Hub struct {
	fanout realtime.Fanout
	log    *slog.Logger
	cfg    Config

	mu   sync.Mutex
	subs map[int64]*accountSub
}

func NewHub(f realtime.Fanout, log *slog.Logger, cfg Config) *Hub {
	return &Hub{
		fanout: f,
		log:    log,
		cfg:    cfg,
		subs:   map[int64]*accountSub{},
	}
}

// Join registers a socket and starts delivering the account's events to it.
func (h *Hub) Join(accountID int64) (*Client, error) {
	c := &Client{
		accountID: accountID,
		out:       make(chan []byte, h.cfg.SendBuffer),
		done:      make(chan struct{}),
	}

	h.mu.Lock()
	sub, existing := h.subs[accountID]
	if existing {
		if len(sub.clients) >= h.cfg.MaxPerAccount {
			h.mu.Unlock()
			return nil, ErrTooManySockets
		}
		sub.clients[c] = struct{}{}
		h.mu.Unlock()
		return c, nil
	}
	// Claim the slot before subscribing so two concurrent joins cannot both decide
	// they are the first and open two subscriptions.
	sub = &accountSub{clients: map[*Client]struct{}{c: {}}}
	h.subs[accountID] = sub
	h.mu.Unlock()

	cancel, err := h.fanout.OnBroadcast(realtime.AccountSubject(accountID), func(b []byte) {
		h.dispatch(accountID, b)
	})
	if err != nil {
		h.mu.Lock()
		var stranded []*Client
		if current, ok := h.subs[accountID]; ok && current == sub {
			// Everyone who joined behind us was relying on this subscription, so they are
			// stranded too. Close them: a socket attached to a subscription that never
			// opened stays up and silently receives nothing for ever, and its Leave finds
			// no entry to clean up. A refused connect is far better — the client retries.
			for other := range sub.clients {
				stranded = append(stranded, other)
			}
			delete(h.subs, accountID)
		}
		h.mu.Unlock()
		for _, other := range stranded {
			other.close()
		}
		return nil, err
	}

	h.mu.Lock()
	current, ok := h.subs[accountID]
	if ok && current == sub {
		sub.cancel = cancel
		h.mu.Unlock()
		return c, nil
	}
	// Every socket for this account left while we were subscribing — including ours,
	// since we were one of them. Nothing will ever call this cancel, so call it now
	// rather than leaking the subscription for the life of the process.
	h.mu.Unlock()
	cancel()
	return c, nil
}

// Leave drops a socket, cancelling the account's subscription if it was the last one.
// Calling it twice is safe: a read loop and a write pump both notice a dead socket.
func (h *Hub) Leave(c *Client) {
	h.mu.Lock()
	cancel := h.remove(c)
	h.mu.Unlock()

	c.close()
	if cancel != nil {
		cancel()
	}
}

// Count reports open sockets. It is what the connection gauge samples.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	var n int
	for _, sub := range h.subs {
		n += len(sub.clients)
	}
	return n
}

// dispatch hands one message to every socket of an account.
//
// It runs on the bus's dispatch goroutine, so it must never block: a socket whose
// buffer is full is dropped instead of waited for. Waiting would stall delivery for
// every account this process serves in order to accommodate one slow reader.
func (h *Hub) dispatch(accountID int64, payload []byte) {
	h.mu.Lock()
	sub, ok := h.subs[accountID]
	if !ok {
		h.mu.Unlock()
		return
	}
	var (
		dropped []*Client
		cancels []func()
	)
	for c := range sub.clients {
		select {
		case c.out <- payload:
		default:
			dropped = append(dropped, c)
		}
	}
	for _, c := range dropped {
		if cancel := h.remove(c); cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	h.mu.Unlock()

	for _, c := range dropped {
		h.log.Warn("dropped a websocket that fell behind",
			"account_id", c.accountID, "buffer", h.cfg.SendBuffer)
		c.close()
	}
	for _, cancel := range cancels {
		cancel()
	}
}

// remove unregisters c and returns the subscription's cancel when c was the last
// socket for its account. Callers must hold h.mu.
func (h *Hub) remove(c *Client) func() {
	sub, ok := h.subs[c.accountID]
	if !ok {
		return nil
	}
	if _, member := sub.clients[c]; !member {
		return nil
	}
	delete(sub.clients, c)
	if len(sub.clients) > 0 {
		return nil
	}
	delete(h.subs, c.accountID)
	return sub.cancel
}

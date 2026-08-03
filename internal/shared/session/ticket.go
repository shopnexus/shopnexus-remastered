package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/errx"
)

// ErrInvalidTicket covers a ticket that never existed, already ran, or expired.
// Indistinguishable on purpose: the three are the same fact to a client, and telling
// them apart would confirm that a ticket seen in a log was once real.
var ErrInvalidTicket = errx.NewError(http.StatusUnauthorized, "invalid_ticket", "ticket is not valid")

const ticketKeyPrefix = "ws-ticket:"

// ticketPrefix marks a ticket in a log or a URL as what it is. It is not an opaque
// id, so it does not go through shared/id's cipher: an id is a permanent name and
// this is a secret that dies in thirty seconds.
const ticketPrefix = "wst_"

// Tickets hands out single-use handshake credentials for the WebSocket.
//
// A browser cannot set Authorization on new WebSocket(), and putting the access token
// in the query string writes a live fifteen-minute credential into every proxy log,
// Loki and the user's history. A ticket is the same trade every one-time secret in
// this codebase makes: a Redis key with a TTL, read exactly once and then gone.
type Tickets struct {
	cache cache.Client
	ttl   time.Duration
}

func NewTickets(c cache.Client, ttl time.Duration) *Tickets {
	return &Tickets{cache: c, ttl: ttl}
}

// ticket is what a ticket key holds: the session as well as the account, because the
// handshake has to re-check that the session is still alive.
type ticket struct {
	AccountID int64  `json:"account_id"`
	SessionID string `json:"session_id"`
}

// Issue mints a ticket for a caller the normal Bearer middleware has already
// authenticated.
func (t *Tickets) Issue(ctx context.Context, accountID int64, sessionID string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	tok = ticketPrefix + tok
	rec := ticket{AccountID: accountID, SessionID: sessionID}
	if err := t.cache.Set(ctx, ticketKeyPrefix+tok, rec, t.ttl); err != nil {
		return "", fmt.Errorf("store ws ticket: %w", err)
	}
	return tok, nil
}

// Redeem consumes a ticket and answers who it belongs to.
//
// The caller must still check the session: a ticket issued a moment before a logout
// is a valid ticket for a dead session, and letting it open a socket rebuilds exactly
// the hole middleware.Auth pays a Redis lookup per request to close.
func (t *Tickets) Redeem(ctx context.Context, tok string) (int64, string, error) {
	if tok == "" {
		return 0, "", ErrInvalidTicket
	}
	var rec ticket
	if err := t.cache.GetDel(ctx, ticketKeyPrefix+tok, &rec); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return 0, "", ErrInvalidTicket
		}
		return 0, "", fmt.Errorf("read ws ticket: %w", err)
	}
	if rec.AccountID == 0 || rec.SessionID == "" {
		return 0, "", ErrInvalidTicket
	}
	return rec.AccountID, rec.SessionID, nil
}

// TTL returns the ticket's time-to-live, so the caller can tell the client how long
// the ticket is valid for.
func (t *Tickets) TTL() time.Duration {
	return t.ttl
}

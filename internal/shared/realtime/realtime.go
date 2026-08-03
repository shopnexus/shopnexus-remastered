// Package realtime is the one place that knows what a pushed event looks like on the
// wire and which subject carries it.
//
// A service records a fact by calling Notify; the gateway's hub decodes with
// DecodeEnvelope. Both sides of the socket therefore agree by construction, and the
// AsyncAPI document describes exactly one shape.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// subjectPrefix namespaces per-account fan-out subjects.
const subjectPrefix = "ws.acct."

// ErrNoRecipient is a programming error surfacing as a value: an event with no
// account to deliver it to has no subject, and silently dropping it would hide the
// bug until somebody noticed a missing badge.
var ErrNoRecipient = errors.New("realtime: event has no recipient account")

// Fanout is the transport an event crosses to reach every gateway replica. Declared
// here rather than imported so shared/ still depends on nothing in infra/;
// *eventbus.NATS satisfies it structurally.
type Fanout interface {
	Broadcast(subject string, payload []byte) error
	OnBroadcast(subject string, h func([]byte)) (cancel func(), err error)
}

// Event binds a code to the payload published with it — the same pairing
// eventbus.Topic[T] makes for the durable bus. Nothing else names the code string, so
// a typo cannot compile and then match nothing.
type Event[T any] struct{ Code string }

func NewEvent[T any](code string) Event[T] { return Event[T]{Code: code} }

// Envelope is what travels: a code a client switches on, the instant the backend
// published it, and the payload. Data stays deferred so the hub can forward bytes it
// never needs to understand.
type Envelope struct {
	Code string          `json:"code"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

// AccountSubject is the subject carrying one account's events. Per-account rather than
// one shared subject filtered in process, so NATS does the filtering and a replica
// holding no socket for that account receives no bytes at all.
func AccountSubject(accountID int64) string {
	return subjectPrefix + strconv.FormatInt(accountID, 10)
}

// Notify pushes one fact to every live socket of accountID.
//
// Callers treat this as best-effort: the row is already committed, so an unreachable
// bus is a stale interface, never a failed request. It returns an error anyway,
// because whether to log or to retry is the caller's call, not this package's.
//
// One account per call. A fact with two interested parties is two calls, because the
// caller is the only side that knows the relationship — and the subject is the whole
// authorisation, so addressing is not something to guess at here.
func Notify[T any](ctx context.Context, f Fanout, accountID int64, e Event[T], data T) error {
	if accountID == 0 {
		return fmt.Errorf("%w: code %s", ErrNoRecipient, e.Code)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", e.Code, err)
	}
	body, err := json.Marshal(Envelope{Code: e.Code, At: time.Now().UTC(), Data: raw})
	if err != nil {
		return fmt.Errorf("encode %s envelope: %w", e.Code, err)
	}
	if err := f.Broadcast(AccountSubject(accountID), body); err != nil {
		return fmt.Errorf("broadcast %s: %w", e.Code, err)
	}
	_ = ctx // the transport is fire-and-forget; the parameter keeps call sites uniform
	return nil
}

// DecodeEnvelope parses a fan-out message.
func DecodeEnvelope(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode realtime envelope: %w", err)
	}
	if env.Code == "" {
		return Envelope{}, errors.New("realtime: envelope has no code")
	}
	return env, nil
}

// DataOf reads an envelope's payload as e's type, answering false for a different
// event — so nobody decodes the wrong shape out of a raw message.
func DataOf[T any](env Envelope, e Event[T]) (T, bool) {
	var zero T
	if env.Code != e.Code {
		return zero, false
	}
	var out T
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return zero, false
	}
	return out, true
}

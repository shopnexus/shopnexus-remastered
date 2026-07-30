// Package session keeps sign-in sessions in Redis so revoking one is a key
// delete rather than a row update.
//
// An access token is a JWT naming the account *and* the session; every
// authenticated request looks the session up here, which is what makes a logout,
// a password change or a suspension effective against a token already in
// circulation. A refresh token is a second key pointing at the same session and
// is rotated on every exchange, so a stolen one is usable at most once.
//
// Revoking every session of an account does not need the list of its sessions:
// each record carries the account's epoch at the time it was created, and
// bumping the epoch invalidates all of them at once. That keeps the write O(1)
// and needs no set type in the cache contract.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/errx"
)

// ErrInvalidSession covers every reason a session cannot be used: never existed,
// expired, logged out, or superseded by an epoch bump. They are deliberately
// indistinguishable to the caller.
var ErrInvalidSession = errx.NewError(http.StatusUnauthorized, "invalid_session", "session is not valid")

const (
	sessionKeyPrefix = "session:"
	refreshKeyPrefix = "refresh:"
	epochKeyPrefix   = "session-epoch:"
)

// Session is a live sign-in. RefreshToken is only set on the call that minted it
// — it is a credential, so it is never read back out of the store.
type Session struct {
	ID           string
	AccountID    int64
	RefreshToken string
	ExpiresAt    time.Time
}

// record is what a session key holds. Epoch is the account's epoch when the
// session was created; a mismatch with the account's current epoch means the
// session was revoked in bulk.
type record struct {
	AccountID int64 `json:"account_id"`
	Epoch     int64 `json:"epoch"`
}

// Store is the session store. TTL is how long a session lives without being
// refreshed; the access token's own lifetime is shorter and belongs to
// token.Manager.
type Store struct {
	cache cache.Client
	ttl   time.Duration
}

func New(c cache.Client, ttl time.Duration) *Store {
	return &Store{cache: c, ttl: ttl}
}

// Create opens a session for an account and returns it with a fresh refresh token.
func (s *Store) Create(ctx context.Context, accountID int64) (Session, error) {
	epoch, err := s.epoch(ctx, accountID)
	if err != nil {
		return Session{}, err
	}
	sid, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	if err := s.cache.Set(ctx, sessionKeyPrefix+sid, record{AccountID: accountID, Epoch: epoch}, s.ttl); err != nil {
		return Session{}, fmt.Errorf("store session: %w", err)
	}
	refresh, err := s.issueRefresh(ctx, sid)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: sid, AccountID: accountID, RefreshToken: refresh, ExpiresAt: time.Now().Add(s.ttl)}, nil
}

// Lookup resolves a session id to its account, and is the check on the request
// path. It returns ErrInvalidSession for a session that is gone or superseded.
func (s *Store) Lookup(ctx context.Context, sessionID string) (int64, error) {
	rec, err := s.record(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	return rec.AccountID, nil
}

// Rotate exchanges a refresh token for a new one on the same session, so the
// access token that follows keeps the session the client already has. The old
// refresh token stops working.
func (s *Store) Rotate(ctx context.Context, refreshToken string) (Session, error) {
	var sid string
	if err := s.cache.Get(ctx, refreshKeyPrefix+refreshToken, &sid); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return Session{}, ErrInvalidSession
		}
		return Session{}, fmt.Errorf("read refresh token: %w", err)
	}
	rec, err := s.record(ctx, sid)
	if err != nil {
		return Session{}, err
	}
	if err := s.cache.Delete(ctx, refreshKeyPrefix+refreshToken); err != nil {
		return Session{}, fmt.Errorf("drop used refresh token: %w", err)
	}
	// Refreshing extends the session: a client that keeps using it should not be
	// signed out on the anniversary of its first sign-in.
	if err := s.cache.Set(ctx, sessionKeyPrefix+sid, rec, s.ttl); err != nil {
		return Session{}, fmt.Errorf("extend session: %w", err)
	}
	next, err := s.issueRefresh(ctx, sid)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: sid, AccountID: rec.AccountID, RefreshToken: next, ExpiresAt: time.Now().Add(s.ttl)}, nil
}

// Revoke ends one session — a logout. Unknown ids succeed: the caller wanted the
// session gone and it is.
func (s *Store) Revoke(ctx context.Context, sessionID string) error {
	if err := s.cache.Delete(ctx, sessionKeyPrefix+sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// RevokeAll ends every session of an account by bumping its epoch. keep, when
// non-empty, is re-stamped with the new epoch and survives — that is "change the
// password and sign out everywhere else".
func (s *Store) RevokeAll(ctx context.Context, accountID int64, keep string) error {
	epoch, err := s.epoch(ctx, accountID)
	if err != nil {
		return err
	}
	next := epoch + 1
	// No expiry: an epoch that lapsed while a session was still alive would read
	// back as 0 and let a revoked session through.
	if err := s.cache.Set(ctx, epochKey(accountID), next, 0); err != nil {
		return fmt.Errorf("bump session epoch: %w", err)
	}
	if keep == "" {
		return nil
	}
	if err := s.cache.Set(ctx, sessionKeyPrefix+keep, record{AccountID: accountID, Epoch: next}, s.ttl); err != nil {
		return fmt.Errorf("restamp kept session: %w", err)
	}
	return nil
}

// record reads a session and checks it against the account's current epoch.
func (s *Store) record(ctx context.Context, sessionID string) (record, error) {
	var rec record
	if sessionID == "" {
		return record{}, ErrInvalidSession
	}
	if err := s.cache.Get(ctx, sessionKeyPrefix+sessionID, &rec); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return record{}, ErrInvalidSession
		}
		return record{}, fmt.Errorf("read session: %w", err)
	}
	epoch, err := s.epoch(ctx, rec.AccountID)
	if err != nil {
		return record{}, err
	}
	if rec.Epoch != epoch {
		// Superseded: drop the key so the next request costs one lookup less.
		_ = s.cache.Delete(ctx, sessionKeyPrefix+sessionID)
		return record{}, ErrInvalidSession
	}
	return rec, nil
}

// epoch reads the account's session epoch; an account that was never revoked has
// no key and starts at zero.
func (s *Store) epoch(ctx context.Context, accountID int64) (int64, error) {
	var epoch int64
	err := s.cache.Get(ctx, epochKey(accountID), &epoch)
	if err == nil {
		return epoch, nil
	}
	if errors.Is(err, cache.ErrCacheMiss) {
		return 0, nil
	}
	return 0, fmt.Errorf("read session epoch: %w", err)
}

func (s *Store) issueRefresh(ctx context.Context, sessionID string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := s.cache.Set(ctx, refreshKeyPrefix+tok, sessionID, s.ttl); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}
	return tok, nil
}

// randomToken is 32 hex chars of crypto/rand — the same shape for a session id
// and a refresh token, since both are unguessable handles and nothing else.
func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func epochKey(accountID int64) string { return epochKeyPrefix + strconv.FormatInt(accountID, 10) }

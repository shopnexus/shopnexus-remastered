// Package token issues and verifies the access token (JWT, HS256).
//
// The token names two things: the account it belongs to (subject) and the session
// it was minted in (jti). The session is what makes revocation possible — a
// signature alone is valid until it expires, and a logout or a suspension has to
// take effect before that. Checking the session id against the store is the
// gateway's job; this package only carries it.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is what an access token asserts. AccountID is the *opaque* account id: a
// JWT is readable by whoever holds it, so the raw database key never goes in.
type Claims struct {
	AccountID string
	SessionID string
}

type Manager struct {
	secret []byte
	ttl    time.Duration
}

func NewManager(secret string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl}
}

// TTL is the access token's lifetime, which an auth response reports as expires_in.
func (m *Manager) TTL() time.Duration { return m.ttl }

func (m *Manager) Issue(c Claims) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   c.AccountID,
		ID:        c.SessionID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.ttl)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

func (m *Manager) Parse(tokenStr string) (Claims, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return Claims{}, fmt.Errorf("parse token: %w", err)
	}
	if claims.Subject == "" {
		return Claims{}, errors.New("token missing subject")
	}
	if claims.ID == "" {
		return Claims{}, errors.New("token missing session id")
	}
	return Claims{AccountID: claims.Subject, SessionID: claims.ID}, nil
}

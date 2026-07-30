// Package gwctx stores and reads the userID and requestID in the context.
package gwctx

import (
	"context"

	"shopnexus/internal/shared/id"
)

type ctxKey int

const (
	userIDKey ctxKey = iota
	requestIDKey
	sessionIDKey
)

// The user id arrives decoded: the auth middleware parses the token subject once,
// so a handler drops it straight into a DTO field and no handler has to decide
// what a malformed subject means.
func WithUserID(ctx context.Context, uid id.ID[id.Account]) context.Context {
	return context.WithValue(ctx, userIDKey, uid)
}

func UserID(ctx context.Context) (id.ID[id.Account], bool) {
	v, ok := ctx.Value(userIDKey).(id.ID[id.Account])
	return v, ok
}

// The session id travels beside the user id because two endpoints are about the
// session itself rather than about the account: a logout revokes this one, and a
// password change revokes every *other* one. Both need to know which one is calling,
// and neither may take it from the body.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

func SessionID(ctx context.Context) string {
	v, _ := ctx.Value(sessionIDKey).(string)
	return v
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

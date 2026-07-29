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

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

package svc

import (
	"context"

	restate "github.com/restatedev/sdk-go"
)

//go:generate go run shopnexus-server/cmd/genrestate -interface SvcBiz -service Svc
type SvcBiz interface {
	GetThing(ctx context.Context, id int64) (string, error) // query → flat
	DoThing(ctx restate.Context, id int64) error            // command → Call/Send/Future
}

package svc

import "context"

//go:generate go run shopnexus-server/cmd/genrestate -interface SvcBiz -service Svc
type SvcBiz interface {
	GetThing(ctx context.Context, id int64) (string, error)
	DoThing(ctx context.Context, id int64) error
}

package search

import (
	"context"
	"time"
)

type SearchSyncer interface {
	GetLastSearchEngineSyncTime() (time.Time, error)
	SetLastSearchEngineSyncTime(time.Time) error
}

type Client interface {
	IndexDocuments(ctx context.Context, index string, id string, docs any) error
	UpdateDocument(ctx context.Context, index string, id string, doc any) error
	DeleteDocument(ctx context.Context, index, id string) error

	Search(ctx context.Context, index string, query string) ([]SearchResult, error)
	Suggest(ctx context.Context, index string, query string) ([]string, error)

	Close() error
}

type SearchResult struct {
	ID    string
	Score float64
}

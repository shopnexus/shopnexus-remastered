package catalogapi

import "shopnexus/internal/shared/id"

// Category is one node of the browse tree. The client assembles the shape from
// ParentID, because a tree this small costs less to send flat than to nest.
type Category struct {
	ID          id.ID[id.Category]  `json:"id"`
	ParentID    *id.ID[id.Category] `json:"parent_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	// Score is set only by a `near` query, where the answer is a ranking rather than
	// the tree.
	Score *float64 `json:"score,omitempty"`
}

// Tag is a label on a listing. Its slug is its id — a natural key, so it is readable
// rather than encoded.
type Tag struct {
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	// Score is set only by a `near` query.
	Score *float64 `json:"score,omitempty"`
}

// PageInfo is the page-paginated meta every catalog list answers with. TotalCount is nil
// for a ranked query, where the top-K is all the search ever visited.
//
// Field for field identical to httpx.PageMeta, so a handler converts rather than maps:
// `httpx.PageMeta(res.Meta)`.
type PageInfo struct {
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
	TotalCount *int64 `json:"total_count"`
}

type TagPage struct {
	Data []Tag    `json:"data"`
	Meta PageInfo `json:"meta"`
}

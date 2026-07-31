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

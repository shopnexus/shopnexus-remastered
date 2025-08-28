package sharedmodel

// PaginationParams represents the pagination parameters
type PaginationParams struct {
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
}

func (p *PaginationParams) Offset() int32 {
	return (p.Page - 1) * p.Limit
}

func (p *PaginationParams) NextPage(total int64) *int32 {
	if int64(p.Page*p.Limit) < total {
		nextPage := p.Page + 1
		return &nextPage
	}

	return nil
}

// PaginateResult represents a paginated result set
type PaginateResult[T any] struct {
	Data       []T     `json:"data"`
	Limit      int32   `json:"limit"`
	Page       int32   `json:"page"`
	Total      int64   `json:"total"`
	NextPage   *int32  `json:"next_page,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

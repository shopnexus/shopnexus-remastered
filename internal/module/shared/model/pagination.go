package sharedmodel

// PaginationParams represents the pagination parameters
type PaginationParams struct {
	Page  int32 `query:"page" validate:"omitempty,gt=0"`
	Limit int32 `query:"limit" validate:"omitempty,gt=0,lte=100"`
}

func (p *PaginationParams) GetOffset() int32 {
	if p.Page <= 0 {
		p.Page = 1 // default page
	}
	if p.Limit <= 0 {
		p.Limit = 10 // default limit
	}

	offset := (p.Page - 1) * p.Limit
	if offset < 0 {
		return 0
	}
	return offset
}

func (p *PaginationParams) GetPage() int32 {
	if p.Page <= 0 {
		return 1 // default page
	}
	return p.Page
}

func (p *PaginationParams) GetLimit() int32 {
	if p.Limit <= 0 {
		return 10 // default limit
	}
	if p.Limit > 100 {
		return 100 // max limit
	}
	return p.Limit
}

func (p *PaginationParams) NextPage(total int64) *int32 {
	if p.Page <= 0 {
		p.Page = 0
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}

	if int64(p.Page*p.Limit) < total {
		nextPage := p.Page + 1
		return &nextPage
	}

	return nil
}

func (p *PaginationParams) NextCursor(lastID int64) *string {

	// Cursor pagination is not implemented yet
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

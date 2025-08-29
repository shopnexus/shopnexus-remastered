package response

type PaginationResponse[T any] struct {
	Data     []T      `json:"data"`
	PageMeta PageMeta `json:"pagination"`
}

type PageMeta struct {
	Page       int32   `json:"page"`
	Limit      int32   `json:"limit"`
	Total      int64   `json:"total"`
	NextPage   *int32  `json:"next_page,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

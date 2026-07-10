package response

import "github.com/guregu/null/v6"

type PaginationResponse[T any] struct {
	Data     []T      `json:"data"`
	PageMeta PageMeta `json:"pagination"`
}

// SwaggerPaginationResponse is a non-generic mirror of PaginationResponse[T]
// used ONLY in swaggo annotations: swaggo cannot resolve a generic type argument
// whose package the transport file doesn't import, but it DOES resolve types in a
// `{data=...}` field override globally. Annotate paginated endpoints as
// `response.SwaggerPaginationResponse{data=[]pkg.Item}`. Not used at runtime.
type SwaggerPaginationResponse struct {
	Data     []any    `json:"data"`
	PageMeta PageMeta `json:"pagination"`
}

type PageMeta struct {
	Limit      null.Int32  `json:"limit"`
	Total      null.Int64  `json:"total"`
	Page       null.Int32  `json:"page"`
	NextPage   null.Int32  `json:"next_page"`
	Cursor     null.String `json:"cursor"`
	NextCursor null.String `json:"next_cursor"`
}

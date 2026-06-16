package ordermodel

// WithTotal pairs a row with the windowed COUNT(*) OVER() total used by the
// offset-paginated list queries (seller refunds, disputes, seller orders, …).
type WithTotal[T any] struct {
	Row        T
	TotalCount int64
}

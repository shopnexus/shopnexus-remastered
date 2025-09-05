package pgutil

// SliceToPgArray converts a slice of values to a pgtype array
func SliceToPgArray[T any, P any](slice []T, converter func(T) P) []P {
	result := make([]P, len(slice))
	for i, item := range slice {
		result[i] = converter(item)
	}
	return result
}

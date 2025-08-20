package slice

func Diff[T comparable](a, b []T) (added, removed []T) {
	aMap := make(map[T]struct{}, len(a))

	for _, item := range a {
		aMap[item] = struct{}{}
	}

	// Track items that are in b but not in a
	for _, item := range b {
		if _, exists := aMap[item]; !exists {
			added = append(added, item)
		} else {
			// Remove from aMap to avoid unnecessary iteration later
			delete(aMap, item)
		}
	}

	// Remaining items in aMap are the removed ones
	for item := range aMap {
		removed = append(removed, item)
	}

	return added, removed
}

func Map[T, U any](slice []T, transform func(T) U) []U {
	result := make([]U, len(slice))
	for i, v := range slice {
		result[i] = transform(v)
	}
	return result
}

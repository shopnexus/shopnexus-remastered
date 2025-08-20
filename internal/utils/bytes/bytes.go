package bytes

// OrByteSlices merges two byte slices representing bit arrays using bitwise OR.
func OrByteSlices(a, b []byte) []byte {
	lenA, lenB := len(a), len(b)
	maxLen := max(lenB, lenA)
	result := make([]byte, maxLen)

	for i := range maxLen {
		var byteA, byteB byte
		if i < lenA {
			byteA = a[i]
		}
		if i < lenB {
			byteB = b[i]
		}
		result[i] = byteA | byteB
	}

	return result
}

package ptr

import (
	"time"
)

func ToPtr[T any](v T) *T {
	return &v
}

func DerefOrNil[T any](v *T) any {
	if v == nil {
		return nil
	}

	return *v
}

func DerefDefault[T any](v *T, d T) T {
	if v == nil {
		return d
	}

	return *v
}

// PtrMilisToTime converts a millisecond timestamp to a time.Time pointer
func PtrMilisToTime(v *int64) *time.Time {
	if v == nil {
		return nil
	}
	return ToPtr(time.UnixMilli(*v))
}

// PtrTimeToMilis converts a time.Time pointer to a millisecond timestamp
func PtrTimeToMilis(v *time.Time) *int64 {
	if v == nil {
		return nil
	}

	return ToPtr(v.UnixMilli())
}

func BrandedToStringPtr[T ~string](b *T) *string {
	if b == nil {
		return nil
	}
	str := string(*b)
	return &str
}

// Convert transforms a pointer to type Source into a pointer to type Target
// using the provided transformation function. If the input pointer is nil,
// it returns nil without calling the transformation function.
//
// Example:
//
//	intPtr := &42
//	strPtr := Convert(intPtr, strconv.Itoa)  // *string("42")
//
//	var nilInt *int
//	nilStr := Convert(nilInt, strconv.Itoa)  // nil
func Convert[Source any, Target any](ptr *Source, transform func(Source) Target) *Target {
	if ptr == nil {
		return nil
	}

	result := transform(*ptr)
	return &result
}

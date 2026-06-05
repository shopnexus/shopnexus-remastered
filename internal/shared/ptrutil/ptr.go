package ptrutil

func Ptr[T any](v T) *T { return &v }

func PtrIf[T any](val T, valid bool) *T {
	if !valid {
		return nil
	}
	return &val
}

package pgxptr

import (
	"database/sql"
	"database/sql/driver"
)

// PgtypeToPtr converts a pgtype to a pointer value
func PgtypeToPtr[T any](v driver.Valuer) *T {
	data, err := v.Value()
	if err != nil {
		return nil
	}

	if data == nil {
		return nil
	}

	if val, ok := data.(T); ok {
		return &val
	}

	return nil
}

// PtrToPgtype convert a pointer value to a pgtype, panics if an error occurs
//
// Branded types should be converted to string before calling this function (e.g. string(myType)).
// Timestamptz should be converted to time.Time before calling this function
func PtrToPgtype[T sql.Scanner, G any](base T, v *G) T {
	if v == nil {
		return base
	}

	// If branded type is not converted to string before calling this function, it will error since pgx cannot Scan our type
	err := base.Scan(*v)
	if err != nil {
		panic(err)
	}
	return base
}

// ValueToPgtype convert a value to a pgtype, panics if an error occurs
//
// Branded types should be converted to string before calling this function (e.g. string(myType))
// Timestamptz should be converted to time.Time before calling this function
func ValueToPgtype[T sql.Scanner, G any](base T, v G) T {
	// If branded type is not converted to string before calling this function, it will error since pgx cannot Scan our type
	err := base.Scan(v)
	if err != nil {
		panic(err)
	}
	return base
}

// PtrBrandedToPgType convert a pointer branded type to a pgtype, panics if an error occurs
//
// Branded type should use this over *PtrToPgtype
func PtrBrandedToPgType[T sql.Scanner, G ~string](base T, v *G) T {
	if v == nil {
		return base
	}

	err := base.Scan(string(*v))
	if err != nil {
		panic(err)
	}
	return base
}

// BrandedToPgType convert a branded type to a pgtype, panics if an error occurs
//
// Branded type should use this over ToPgtype
func BrandedToPgType[T sql.Scanner, G ~string](base T, v G) T {
	err := base.Scan(string(v))
	if err != nil {
		panic(err)
	}
	return base
}

package pgutil

import "github.com/jackc/pgx/v5/pgtype"

func StringToPgText(strings string) pgtype.Text {
	return pgtype.Text{String: strings, Valid: true}
}

func Int64ToPgInt8(n int64) pgtype.Int8 {
	return pgtype.Int8{Int64: n, Valid: true}
}

func Int32ToPgInt4(n int32) pgtype.Int4 {
	return pgtype.Int4{Int32: n, Valid: true}
}

func BoolToPgBool(b bool) pgtype.Bool {
	return pgtype.Bool{Bool: b, Valid: true}
}

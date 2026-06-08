package paginate

import (
	"encoding/base64"
	"encoding/json"

	"github.com/guregu/null/v6"
)

type Params struct {
	Page   null.Int32  `query:"page"   validate:"omitnil,gt=0"`
	Cursor null.String `query:"cursor" validate:"omitnil"`
	Limit  null.Int32  `query:"limit"  validate:"omitnil,gt=0"`
	Sort   string      `query:"sort"` // e.g. -date_created,score; presence (or cursor) => keyset mode
}

// TODO: sau khi sửa xong null.X thì xoá luôn hàm này
func (p Params) Constrain() Params {
	if p.Limit.Valid {
		if p.Limit.Int32 > 100 {
			p.Limit.SetValid(100)
		}
	} else {
		p.Limit.SetValid(10)
	}

	if !p.Page.Valid {
		p.Page.SetValid(1)
	}
	return p
}

func (p Params) Offset() null.Int32 {
	if p.Limit.Valid {
		offset := (p.Page.Int32 - 1) * p.Limit.Int32
		if offset < 0 {
			return null.Int32{}
		}
		return null.Int32From(offset)
	}

	return null.Int32{}
}

func (p Params) DecodeCursor(dst any) error {
	if !p.Cursor.Valid {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(p.Cursor.String)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, dst)
}

type PaginateResult[T any] struct {
	PageParams Params
	Data       []T
	Total      null.Int64  // Only valid for page-based (offset) pagination.
	NextCursor null.String // Already-encoded keyset cursor; only valid for cursor mode.
}

func (p PaginateResult[T]) NextPage() null.Int32 {
	if p.Total.Valid {
		if !p.PageParams.Limit.Valid {
			return null.Int32{}
		}

		page := max(p.PageParams.Page.Int32, 1)
		if int64(page*p.PageParams.Limit.Int32) < p.Total.Int64 {
			return null.Int32From(page + 1)
		}
	}
	return null.Int32{}
}

// EncodeNextCursor returns the already-encoded keyset cursor (set by the repo
// layer via EncodeKeyset). Kept as a method so response helpers stay unchanged.
func (p PaginateResult[T]) EncodeNextCursor() null.String {
	return p.NextCursor
}

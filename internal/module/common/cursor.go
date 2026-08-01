package common

import (
	"net/http"
	"strconv"
	"strings"

	"shopnexus/internal/shared/errx"
)

// ErrCursorInvalid is a page marker this API did not issue. Here rather than in each module's
// domain because the format is shared: three modules had their own copy of the same error, the
// same code and three different separators.
var ErrCursorInvalid = errx.NewError(http.StatusBadRequest, "cursor_invalid",
	"the cursor is not one this endpoint issued")

// cursorSeparator splits the two halves. Opaque to a client either way, so which character it is
// only matters in that every module now uses the same one.
const cursorSeparator = ":"

// FormatCursor is where a page ended: the row's sort key *and* its id, as a tuple.
//
// Both halves are needed. CURRENT_TIMESTAMP is transaction-scoped, so rows written together share
// a timestamp exactly — the three lines of one checkout do — and a key-only cursor makes whichever
// of them the page did not reach unreachable for good. A time key is passed as UnixNano.
func FormatCursor(key, rowID int64) string {
	return strconv.FormatInt(key, 10) + cursorSeparator + strconv.FormatInt(rowID, 10)
}

// ParseCursor reads one back. An empty cursor is the first page, not an error.
func ParseCursor(cursor string) (key, rowID int64, err error) {
	if cursor == "" {
		return 0, 0, nil
	}
	left, right, ok := strings.Cut(cursor, cursorSeparator)
	if !ok {
		return 0, 0, ErrCursorInvalid
	}
	if key, err = strconv.ParseInt(left, 10, 64); err != nil {
		return 0, 0, ErrCursorInvalid
	}
	if rowID, err = strconv.ParseInt(right, 10, 64); err != nil || rowID <= 0 {
		return 0, 0, ErrCursorInvalid
	}
	return key, rowID, nil
}

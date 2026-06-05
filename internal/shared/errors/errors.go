package errors

import (
	"fmt"
	"net/http"

	restate "github.com/restatedev/sdk-go"
)

// Errorf is a format-string error template. Call Fmt to build the terminal error.
type Errorf struct {
	HTTPStatus uint16 `json:"http_status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e Errorf) Fmt(args ...any) error {
	return newCoded(e.HTTPStatus, e.Code, fmt.Errorf(e.Message, args...))
}

// codedError carries the app code: errors.As finds it same-process, the trailing " [code]" tag survives a Restate hop.
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() + " [" + e.code + "]" }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) Code() string  { return e.code }

func newCoded(status uint16, code string, err error) error {
	return restate.TerminalError(&codedError{code: code, err: err}, restate.Code(status))
}

func NewError(status uint16, code string, message string) error {
	// if status not in 4xx-5xx, panic
	if status < 400 || status >= 600 {
		panic(fmt.Sprintf("invalid HTTP status for error: %d", status))
	}
	if code == "" {
		panic("error code cannot be empty")
	}

	return newCoded(status, code, fmt.Errorf("%s", message))
}

func NewErrorf(status uint16, code string, format string) Errorf {
	return Errorf{
		HTTPStatus: status,
		Code:       code,
		Message:    format,
	}
}

var (
	ErrValidation     = NewErrorf(http.StatusBadRequest, "validation", "%s")
	ErrEntityNotFound = NewErrorf(http.StatusNotFound, "entity_not_found", "%s not found")

	ErrDuplicateIdempotencyKey = NewError(http.StatusConflict, "duplicate_idempotency_key", "idempotency key already claimed")
)

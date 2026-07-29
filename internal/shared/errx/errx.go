package errx

import (
	"errors"
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
	httpStatus uint16
	code       string
	err        error
}

func (e *codedError) Error() string { return e.err.Error() + " [" + e.code + "]" }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) Code() string  { return e.code }

func newCoded(status uint16, code string, err error) error {
	// Wrap a Restate terminal error so the failure is not retried across a hop,
	// but return the *codedError itself so errors.As can still reach the code
	// same-process (Restate's ToTerminalError does not preserve the chain).
	return &codedError{
		httpStatus: status,
		code:       code,
		err:        restate.ToTerminalError(err, restate.WithErrorCode(restate.Code(status))),
	}
}

// Decompose extracts (status, code, untagged message) from a coded domain error.
func Decompose(err error) (status uint16, code, message string, ok bool) {
	var ce *codedError
	if !errors.As(err, &ce) {
		return 0, "", "", false
	}
	return ce.httpStatus, ce.code, ce.err.Error(), true
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

// Shared, cross-cutting errors — only the ones used across many modules/handlers.
// Template errors (Errorf) carry a "%s" the caller fills via Fmt; static errors
// are ready to return as-is. Anything module-specific (including not-found) lives
// in that module's domain package (e.g. internal/module/account/domain/errors.go).
var (
	ErrValidation = NewErrorf(http.StatusBadRequest, "validation", "%s")

	ErrBadRequestBody = NewError(http.StatusBadRequest, "bad_request_body", "invalid request body")
	ErrUnauthorized   = NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrInvalidToken   = NewError(http.StatusUnauthorized, "invalid_token", "invalid or expired token")
	// ErrInvalidID is returned by shared/id when an opaque id does not decode:
	// wrong or missing kind prefix, wrong length, or a character outside the
	// alphabet. Cross-cutting because every module's ids go through that codec.
	ErrInvalidID = NewError(http.StatusBadRequest, "invalid_id", "invalid id")
	// ErrNotImplemented is returned by a route that the OpenAPI contract declares
	// but nothing implements yet. 501 rather than 404 on purpose: the path is real
	// and documented, so a caller learns that the URL is right and the feature is
	// missing, which are two different bugs to chase.
	ErrNotImplemented = NewError(http.StatusNotImplemented, "not_implemented", "not implemented")
)

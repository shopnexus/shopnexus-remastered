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

// Field is one field of a request that failed validation. Rule is the constraint that
// rejected it, so a client can localise the message instead of showing ours.
type Field struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// codedError carries the app code: errors.As finds it same-process, the trailing " [code]" tag survives a Restate hop.
type codedError struct {
	httpStatus uint16
	code       string
	err        error
	// Per-field detail, set only by NewValidationError. It does not survive a Restate
	// hop — the tag in Error() carries the code across, and nothing more — which is
	// correct: validation happens at the edge, before any hop.
	fields []Field
}

func (e *codedError) Error() string   { return e.err.Error() + " [" + e.code + "]" }
func (e *codedError) Unwrap() error   { return e.err }
func (e *codedError) Code() string    { return e.code }
func (e *codedError) Fields() []Field { return e.fields }

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

// FieldsOf returns the per-field detail of a validation error, or nil for any other
// error. Separate from Decompose so the common path — every error that is not a
// validation failure — does not carry a slice it never uses.
func FieldsOf(err error) []Field {
	var ce *codedError
	if !errors.As(err, &ce) {
		return nil
	}
	return ce.fields
}

// NewValidationError builds the 400 that carries which fields were wrong.
//
// The message stays a summary for logs and for a client that cannot use the detail; the
// fields are what lets a form mark the right boxes. Flattening both into one sentence —
// which is what a single "%s" template forces — is what made a twelve-field form with
// three problems unactionable.
func NewValidationError(message string, fields ...Field) error {
	e := newCoded(http.StatusBadRequest, "validation", fmt.Errorf("%s", message))
	var ce *codedError
	_ = errors.As(e, &ce)
	ce.fields = fields
	return ce
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
// There is no ErrValidation template here on purpose. A validation failure that drops
// which field failed is the shape this API had and stopped having: build one with
// NewValidationError, or let shared/validation.AsError translate the validator's own
// result, which is where the field paths and rules come from.
var (
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

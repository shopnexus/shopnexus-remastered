package response

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime"
	"shopnexus-server/internal/shared/errors"
	"shopnexus-server/internal/shared/paginate"
	"strings"

	restate "github.com/restatedev/sdk-go"
)

const (
	ContentTypeJSON = "application/json"
)

// restateCodePrefix matches "[400] " markers that Restate prepends to terminal
// error messages. We strip every occurrence so the message handed to the FE is
// the human chain ("resolve address country: address could not be located")
// with no transport noise.
var restateCodePrefix = regexp.MustCompile(`\[\d+\]\s+`)

func writeError(w http.ResponseWriter, httpCode int, err error) error {
	e := errors.Errorf{HTTPStatus: uint16(httpCode), Message: err.Error()}

	// Same-process: the coded carrier survives fmt.Errorf wrapping.
	if ce, ok := stderrors.AsType[interface {
		error
		Code() string
	}](err); ok {
		e.Code = ce.Code()
	}

	e.Message = restateCodePrefix.ReplaceAllString(e.Message, "") // strip "[400] "

	// Trailing " [code]" tag survives a Restate hop: peel it off, recover code if lost.
	if strings.HasSuffix(e.Message, "]") {
		if i := strings.LastIndexByte(e.Message, '['); i >= 0 {
			if e.Code == "" {
				e.Code = e.Message[i+1 : len(e.Message)-1]
			}
			e.Message = strings.TrimRight(e.Message[:i], " ")
		}
	}

	data, marshalErr := json.Marshal(CommonResponse{
		Error: &e,
	})
	if marshalErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return marshalErr
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(httpCode)
	_, writeErr := w.Write(data)
	return writeErr
}

func writeResponse(w http.ResponseWriter, httpCode int, dto any) error {
	data, err := MarshalJSONWithEmptyArrays(dto)
	if err != nil {
		return writeError(w, http.StatusInternalServerError, err)
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(httpCode)
	_, writeErr := w.Write(data)
	return writeErr
}

// FromDTO writes a successful response with the provided DTO.
func FromDTO(w http.ResponseWriter, httpCode int, dto any) error {
	return writeResponse(w, httpCode, CommonResponse{
		Data: dto,
	})
}

// FromMessage writes a response with a string message as data.
func FromMessage(w http.ResponseWriter, httpCode int, message string) error {
	return writeResponse(w, httpCode, CommonResponse{
		Data: message,
	})
}

// FromError writes an error response based on the provided error type.
// If the error is a Restate terminal error with a specific code, that code takes precedence over httpCode.
func FromError(w http.ResponseWriter, httpCode int, err error) error {
	if err == nil {
		return FromDTO(w, http.StatusOK, nil)
	}

	// Extract code from Restate terminal errors (e.g. cross-module 409 propagated through ingress)
	if restate.IsTerminalError(err) {
		if code := int(restate.ErrorCode(err)); code >= 400 && code < 600 {
			httpCode = code
		}
	}

	slog.Error("HTTP error",
		slog.Int("http_code", httpCode),
		slog.Any("error", err),
		slog.String("stack", getStackTrace()),
	)

	return writeError(w, httpCode, err)
}

// FromHTTPCode writes a response based on the provided HTTP status code.
func FromHTTPCode(w http.ResponseWriter, httpCode int) error {
	if httpCode < 100 || httpCode > 599 {
		httpCode = http.StatusInternalServerError
	}

	statusText := http.StatusText(httpCode)
	if statusText == "" {
		statusText = "Unknown Error"
	}

	e := errors.NewErrorf(uint16(httpCode), "", statusText)
	response := CommonResponse{
		Error: &e,
	}

	return writeResponse(w, httpCode, response)
}

// FromPaginate writes a paginated response.
func FromPaginate[T any](w http.ResponseWriter, paginate paginate.PaginateResult[T]) error {
	data := paginate.Data
	if data == nil {
		data = make([]T, 0)
	}

	response := PaginationResponse[T]{
		Data: data,
		PageMeta: PageMeta{
			Limit:      paginate.PageParams.Limit,
			Total:      paginate.Total,
			Page:       paginate.PageParams.Page,
			Cursor:     paginate.PageParams.Cursor,
			NextPage:   paginate.NextPage(),
			NextCursor: paginate.EncodeNextCursor(),
		},
	}

	return writeResponse(w, http.StatusOK, response)
}

func getStackTrace() string {
	var pc [32]uintptr
	n := runtime.Callers(2, pc[:]) // skip runtime.Callers and getStackTrace itself

	var builder strings.Builder
	builder.WriteString("Stack trace:\n")

	frames := runtime.CallersFrames(pc[:n])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "runtime/") &&
			!strings.Contains(frame.File, "testing/") {
			fmt.Fprintf(&builder, "  at %s (%s:%d)\n",
				frame.Function, frame.File, frame.Line)
		}
		if !more {
			break
		}
	}
	return builder.String()
}

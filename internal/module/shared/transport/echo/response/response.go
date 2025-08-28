package response

import (
	"net/http"
	"strconv"

	sharedmodel "shopnexus-remastered/internal/module/backup/shared/model"

	"github.com/bytedance/sonic"
)

const (
	ContentTypeJSON   = "application/json"
	ErrValidationCode = "VALIDATION_ERROR"
)

// writeError writes an error response with proper error handling
func writeError(w http.ResponseWriter, httpCode int, err error) error {
	// Default code and message
	errCode := strconv.Itoa(httpCode)
	message := http.StatusText(httpCode)

	// Use the error's message if it implements ErrorWithCode (domain errors)
	if errWithCode, ok := err.(sharedmodel.ErrorWithCode); ok {
		errCode = errWithCode.Code()
		message = errWithCode.Error()
	}

	data, err := sonic.Marshal(CommonResponse{
		Data:  nil,
		Error: sharedmodel.NewError(errCode, message),
	})
	if err != nil {
		// Fallback to plain text if JSON marshaling fails
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(httpCode)
	_, writeErr := w.Write(data)
	return writeErr
}

// writeResponse is the core response writer with better error handling
func writeResponse(w http.ResponseWriter, httpCode int, dto any) error {
	data, err := sonic.Marshal(dto)
	if err != nil {
		return writeError(w, http.StatusInternalServerError, err)
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(httpCode)
	_, writeErr := w.Write(data)
	return writeErr
}

// FromDTO writes a successful response with the provided DTO
func FromDTO(w http.ResponseWriter, httpCode int, dto any) error {
	return writeResponse(w, httpCode, CommonResponse{
		Data: dto,
	})
}

func FromMessage(w http.ResponseWriter, httpCode int, message string) error {
	return writeResponse(w, httpCode, CommonResponse{
		Data: message,
	})
}

// FromError writes an error response based on the provided error type
func FromError(w http.ResponseWriter, httpCode int, err error) error {
	if err == nil {
		return FromDTO(w, http.StatusOK, nil)
	}

	return writeError(w, httpCode, err)
}

// FromHTTPCode writes a response based on the provided HTTP status code
func FromHTTPCode(w http.ResponseWriter, httpCode int) error {
	// Validate HTTP status code
	if httpCode < 100 || httpCode > 599 {
		httpCode = http.StatusInternalServerError
	}

	statusCode := strconv.Itoa(httpCode)
	statusText := http.StatusText(httpCode)

	// Use generic message if status text is empty
	if statusText == "" {
		statusText = "Unknown Error"
	}

	response := CommonResponse{
		Data:  nil,
		Error: sharedmodel.NewError(statusCode, statusText),
	}

	return writeResponse(w, httpCode, response)
}

type Paginate[T any] interface {
	GetData() []T
	GetLimit() int32
	GetNextCursor() *string
	GetNextPage() *int32
	GetPage() int32
	GetTotal() int64
}

// FromPaginate writes a paginated response with proper structure
func FromPaginate[T any](w http.ResponseWriter, paginate Paginate[T]) error {
	data := paginate.GetData()
	if data == nil {
		// Make sure the paginate object is not nil
		data = make([]T, 0)
	}

	response := PaginationResponse[T]{
		Data: data,
		PageMeta: PageMeta{
			Limit:      paginate.GetLimit(),
			Page:       paginate.GetPage(),
			Total:      paginate.GetTotal(),
			NextPage:   paginate.GetNextPage(),
			NextCursor: paginate.GetNextCursor(),
		},
	}

	return writeResponse(w, http.StatusOK, response)
}

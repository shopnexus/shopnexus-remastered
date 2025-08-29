package sharedmodel

type ErrorWithCode interface {
	Error() string
	Code() string
}

type Error struct {
	ErrCode string `json:"code"`
	Message string `json:"message"`
}

func (e Error) Error() string {
	return e.Message
}

func (e Error) Code() string {
	return e.ErrCode
}

func NewError(code, message string) Error {
	return Error{
		ErrCode: code,
		Message: message,
	}
}

package sharedmodel

type ErrorWithCode interface {
	Error() string
	Code() string
}

type Error struct {
	code    string
	message string
}

func (e Error) Error() string {
	return e.message
}

func (e Error) Code() string {
	return e.code
}

func NewError(code, message string) Error {
	return Error{
		code:    code,
		message: message,
	}
}

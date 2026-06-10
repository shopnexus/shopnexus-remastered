package besteffort

import "shopnexus-server/internal/shared/errors"

// Envelope is the wire form of a domain error, carried in non-2xx response bodies.
type Envelope struct {
	HTTPStatus uint16 `json:"http_status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func EncodeError(err error) Envelope {
	if err == nil {
		return Envelope{}
	}
	if status, code, msg, ok := errors.Decompose(err); ok {
		return Envelope{HTTPStatus: status, Code: code, Message: msg}
	}
	return Envelope{HTTPStatus: 500, Code: "internal", Message: err.Error()}
}

func DecodeError(e Envelope) error {
	if e.Code == "" {
		return nil
	}
	return errors.NewError(e.HTTPStatus, e.Code, e.Message)
}

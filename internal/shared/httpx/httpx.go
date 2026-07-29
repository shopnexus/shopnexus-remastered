// Package httpx: JSON helpers + errx -> HTTP mapping. The only place that knows HTTP status.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"shopnexus/internal/shared/errx"
)

func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Error errBody `json:"error"`
}

func WriteError(w http.ResponseWriter, log *slog.Logger, err error) {
	if status, code, message, ok := errx.Decompose(err); ok {
		WriteJSON(w, int(status), envelope{Error: errBody{Code: code, Message: message}})
		return
	}
	log.Error("unhandled error", "err", err)
	WriteJSON(w, http.StatusInternalServerError, envelope{Error: errBody{Code: "internal", Message: "internal error"}})
}

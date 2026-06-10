package besteffort

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// handlerFn decodes the raw JSON body and returns a JSON-marshalable result or a domain error.
type handlerFn func(ctx context.Context, body []byte) (any, error)

// Server routes BestEffort calls over POST /{service}/{method}.
type Server struct {
	mux *http.ServeMux
}

func NewServer() *Server {
	return &Server{mux: http.NewServeMux()}
}

// Handle registers fn at POST /{service}/{method}.
func (s *Server) Handle(service, method string, fn handlerFn) {
	s.mux.HandleFunc("/"+service+"/"+method, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := fn(r.Context(), body)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	})
}

// Handler returns the mux; the caller owns starting the http.Server.
func (s *Server) Handler() http.Handler { return s.mux }

func writeError(w http.ResponseWriter, err error) {
	env := EncodeError(err)
	status := int(env.HTTPStatus)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

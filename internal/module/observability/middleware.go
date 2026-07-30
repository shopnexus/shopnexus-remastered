package observability

import (
	"net/http"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer, so wrapping a response to
// read its status does not cost the handler flushing or hijacking. Embedding alone does
// not: the embedded interface is http.ResponseWriter, so a type assertion for
// http.Flusher or http.Hijacker against this struct fails even when the writer
// underneath implements both. Without it the first streaming endpoint — an SSE feed, a
// websocket upgrade — silently buffers instead.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush is here as well as Unwrap because handlers written the older way assert
// http.Flusher on the writer they were handed rather than going through
// ResponseController.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Middleware records HTTP RED metrics (rate/errors/duration) for each request.
// It wraps the router so it can read the matched ServeMux pattern (r.Pattern)
// as the low-cardinality route label.
func (s *Sink) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		s.RecordHTTP(r.Method, route, rec.status, time.Since(start))
	})
}

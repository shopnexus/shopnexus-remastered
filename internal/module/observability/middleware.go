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

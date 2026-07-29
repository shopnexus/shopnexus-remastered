// Package domain holds the telemetry samples: the entities that travel over the
// bus as JSON and land in the hypertables as rows.
package domain

import (
	"encoding/json"
	"time"
)

// MaxErrorLen caps a stored error string: a pathological error (a long URL, a
// wrapped chain) must not bloat the hypertable.
const MaxErrorLen = 300

// CapError trims an error message to MaxErrorLen.
func CapError(s string) string {
	if len(s) <= MaxErrorLen {
		return s
	}
	return s[:MaxErrorLen] + "…"
}

// Instance is the pod/host that produced a sample. Every signal carries it, so
// "one replica is slow" stays distinguishable from "everything is slow".
// It is set once by the Sink, not by the caller of each Record method.

// HTTPSample is one inbound request (RED: rate/errors/duration). Route is the
// ServeMux pattern, not the raw path, to keep its cardinality low.
type HTTPSample struct {
	TS         time.Time `json:"ts"`
	Instance   string    `json:"instance"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
}

// ProviderCall is one outbound dependency call.
type ProviderCall struct {
	TS         time.Time `json:"ts"`
	Instance   string    `json:"instance"`
	Provider   string    `json:"provider"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
	Failed     bool      `json:"failed"`
	Error      string    `json:"error"`
}

// BusinessEvent mirrors a domain event published on the bus.
type BusinessEvent struct {
	TS       time.Time       `json:"ts"`
	Instance string          `json:"instance"`
	Topic    string          `json:"topic"`
	Payload  json.RawMessage `json:"payload"`
}

// RuntimeSample is one Go runtime snapshot.
type RuntimeSample struct {
	TS             time.Time `json:"ts"`
	Instance       string    `json:"instance"`
	Goroutines     int       `json:"goroutines"`
	HeapAllocBytes int64     `json:"heap_alloc_bytes"`
	HeapInuseBytes int64     `json:"heap_inuse_bytes"`
	GCPauseMs      float64   `json:"gc_pause_ms"`
	NumGC          int64     `json:"num_gc"`
}

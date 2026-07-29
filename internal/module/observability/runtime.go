package observability

import (
	"context"
	"runtime"
	"time"
)

// runtimeSampleInterval is how often Go runtime stats are captured.
const runtimeSampleInterval = 15 * time.Second

// sampleRuntime reads a snapshot of the Go runtime and records it.
func (s *Sink) sampleRuntime() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	lastPauseMs := float64(m.PauseNs[(m.NumGC+255)%256]) / 1e6
	s.RecordRuntime(runtime.NumGoroutine(), m.HeapAlloc, m.HeapInuse, lastPauseMs, uint64(m.NumGC))
}

// SampleLoop samples the runtime on an interval until ctx is cancelled. It is
// also where dropped-sample counts are reported, so a bus outage shows up on a
// fixed cadence instead of once per request.
func (s *Sink) SampleLoop(ctx context.Context) {
	t := time.NewTicker(runtimeSampleInterval)
	defer t.Stop()
	s.sampleRuntime() // one sample at startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sampleRuntime()
			s.reportDropped()
		}
	}
}

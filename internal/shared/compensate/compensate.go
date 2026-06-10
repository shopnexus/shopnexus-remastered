// Package compensate runs best-effort, in-process LIFO compensation for
// non-durable sagas. Unlike shared/saga it has no Restate dependency: there is
// NO journal and NO crash recovery — if the process dies mid-compensation,
// pending compensators are lost. Use only where that trade-off is acceptable;
// durable orchestration belongs in a Restate workflow.
package compensate

import (
	"context"
	"fmt"
)

type Saga struct {
	ctx          context.Context
	compensators []step
}

type step struct {
	name string
	fn   func(context.Context) error
}

func New(ctx context.Context) *Saga { return &Saga{ctx: ctx} }

// Defer appends a compensator. Call BEFORE the action it compensates.
func (s *Saga) Defer(name string, fn func(context.Context) error) {
	s.compensators = append(s.compensators, step{name: name, fn: fn})
}

// Compensate runs deferred compensators LIFO, stopping at the first error.
func (s *Saga) Compensate() error {
	for len(s.compensators) > 0 {
		i := len(s.compensators) - 1
		c := s.compensators[i]
		if err := c.fn(s.ctx); err != nil {
			return fmt.Errorf("compensate %s: %w", c.name, err)
		}
		s.compensators = s.compensators[:i]
	}
	return nil
}

// Clear drops pending compensators. Call on the success path.
func (s *Saga) Clear() { s.compensators = nil }

// Wrap runs fn and compensates on error, returning the original error.
func (s *Saga) Wrap(fn func() error) error {
	if err := fn(); err != nil {
		if cErr := s.Compensate(); cErr != nil {
			return fmt.Errorf("error: %w; compensate error: %w", err, cErr)
		}
		return err
	}
	return nil
}

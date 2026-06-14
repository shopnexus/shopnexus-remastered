package saga

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"
)

// Saga collects compensators in append order and runs them LIFO on failure.
//
// Compensation semantics:
//   - Compensators run STRICTLY SEQUENTIALLY in LIFO order.
//   - If any compensator returns an error, Compensate STOPS immediately and
//     returns that error — remaining compensators are NOT skipped, they stay
//     pending for the next retry.
//   - Compensator bodies are expected to wrap durable side-effects in
//     restate.Run(...) so already-succeeded steps are journaled and skipped on
//     replay. The next retry will resume from the failed step.
//   - Returning a non-terminal error from the workflow handler causes Restate
//     to retry the handler indefinitely, which re-invokes Compensate via the
//     defer/WrapError path; successful compensators replay from journal,
//     failed ones run again.
type Saga struct {
	// TODO: sửa lại dùng context.Context thôi, hoặc chỗ runVoid dùng restate.RunVoid
	ctx          restate.Context
	compensators []step
}

type step struct {
	name string
	fn   func(restate.Context) error
}

// New creates an empty saga bound to the given Restate context.
// Accepts WorkflowContext or plain Context — both satisfy restate.Context.
func New(ctx restate.Context) *Saga {
	return &Saga{ctx: ctx}
}

// Defer appends a compensator. Call BEFORE performing the action it compensates.
func (s *Saga) Defer(name string, fn func(restate.Context) error) {
	s.compensators = append(s.compensators, step{name: name, fn: fn})
}

// Compensate runs all deferred compensators LIFO, returning the first error if any.
func (s *Saga) Compensate() error {
	for len(s.compensators) > 0 {
		i := len(s.compensators) - 1
		c := s.compensators[i]
		// TODO: wrap in restate.RunVoid so each compensator is a journaled step —
		// already-run compensators replay from the journal instead of re-executing.
		if err := c.fn(s.ctx); err != nil {
			return fmt.Errorf("saga compensate %s: %w", c.name, err)
		}
		// Pop only on success — keep failed step at the tail for retry.
		s.compensators = s.compensators[:i]
	}
	return nil
}

// Clear drops all pending compensators. Call on the success exit path.
func (s *Saga) Clear() { s.compensators = nil }

func (s *Saga) Wrap(fn func() error) error {
	if err := fn(); err != nil {
		if restate.IsTerminalError(err) {
			if cErr := s.Compensate(); cErr != nil {
				return fmt.Errorf("workflow error: %w; compensate error: %w", err, cErr)
			}
		}

		return err
	}

	return nil
}

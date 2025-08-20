package saga

import (
	"context"
	"fmt"

	"botmediasaver/internal/logger"
)

// SagaStep defines a single step in a Saga.
type SagaStep struct {
	Name       string
	Action     func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

// Saga defines the sequence of Saga steps.
type Saga struct {
	steps []SagaStep
}

// New creates a new Saga.
func New() *Saga {
	return &Saga{
		steps: make([]SagaStep, 0),
	}
}

// AddStep appends a new step to the Saga.
func (s *Saga) AddStep(name string, action func(ctx context.Context) error, compensate func(ctx context.Context) error) {
	step := SagaStep{
		Name:       name,
		Action:     action,
		Compensate: compensate,
	}
	s.steps = append(s.steps, step)
}

// Execute runs all steps in the Saga. If a step fails, it triggers compensation for all completed steps in reverse order.
func (s *Saga) Execute(ctx context.Context) error {
	var completed []SagaStep

	for _, step := range s.steps {
		if err := step.Action(ctx); err != nil {
			logger.Log.Error(fmt.Sprintf("Saga step '%s' failed: %v", step.Name, err))

			// Rollback in reverse order
			for i := len(completed) - 1; i >= 0; i-- {
				logger.Log.Info(fmt.Sprintf("Compensating step '%s' due to failure in step '%s'", completed[i].Name, step.Name))
				compErr := completed[i].Compensate(ctx)
				if compErr != nil {
					logger.Log.Error(fmt.Sprintf("Compensation for step '%s' failed: %v", completed[i].Name, compErr))
				} else {
					logger.Log.Info(fmt.Sprintf("Compensation for step '%s' succeeded", completed[i].Name))
				}
			}

			return fmt.Errorf("saga failed at step '%s': %w", step.Name, err)
		}

		completed = append(completed, step)
	}

	return nil
}

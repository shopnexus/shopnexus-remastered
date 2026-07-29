// Package validation provides the shared, singleton *validator.Validate.
package validation

import (
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	once     sync.Once
	instance *validator.Validate
)

// Default returns the process-wide validator. It is built once and is safe for
// concurrent use: validator caches struct metadata, so a singleton is both the
// recommended usage and avoids re-parsing tags per call. The gateway injects
// this same instance via fx; the domain reaches it directly.
func Default() *validator.Validate {
	once.Do(func() {
		instance = validator.New(validator.WithRequiredStructEnabled())
	})
	return instance
}

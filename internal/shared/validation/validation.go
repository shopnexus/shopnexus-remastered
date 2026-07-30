// Package validation provides the shared, singleton *validator.Validate.
package validation

import (
	"reflect"
	"strings"
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
		// Report the name the client actually sent, not the Go field name. Without this
		// a request with a bad `email` is told that `Email` is invalid, and a form has
		// nothing to match its inputs against — which makes the per-field detail in an
		// error response useless for the one job it exists to do. Falls back to the Go
		// name for a struct with no json tags, which is every domain entity.
		instance.RegisterTagNameFunc(func(f reflect.StructField) string {
			name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
			if name == "" || name == "-" {
				return f.Name
			}
			return name
		})
	})
	return instance
}

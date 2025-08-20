package common

import (
	"fmt"
	"time"
)

// RetryConfig defines the configuration for the retry logic.
type RetryConfig struct {
	Attempts int
	Delay    time.Duration
}

// DoWithRetry retries the provided function according to the config.
// It recovers from panics and returns the last error if all attempts fail.
func DoWithRetry(cfg RetryConfig, fn func() error) (err error) {
	for i := 0; i < cfg.Attempts; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic recovered: %v", r)
				}
			}()

			err = fn()
		}()

		if err == nil {
			return nil
		}

		if i < cfg.Attempts-1 {
			time.Sleep(cfg.Delay)
		}
	}

	return err
}

// DoWithRetryAndReturn retries the provided function according to the config
func DoWithRetryAndReturn[T any](cfg RetryConfig, fn func() (T, error)) (T, error) {
	var result T
	var err error

	for i := 0; i < cfg.Attempts; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic recovered: %v", r)
				}
			}()

			result, err = fn()
		}()

		if err == nil {
			return result, nil
		}

		if i < cfg.Attempts-1 {
			time.Sleep(cfg.Delay)
		}
	}

	return result, err
}

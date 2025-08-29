package seed

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jaswdr/faker/v2"
)

// isDuplicateKeyError checks if the error is a duplicate key constraint violation
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	if pgErr, ok := err.(*pgconn.PgError); ok {
		// PostgreSQL error code 23505 is unique_violation
		return pgErr.Code == "23505"
	}

	// Fallback: check error message
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "duplicate key") ||
		strings.Contains(errMsg, "unique constraint") ||
		strings.Contains(errMsg, "violates unique")
}

// generateUniqueCode generates a unique code with timestamp to avoid collisions
func generateUniqueCode(fake *faker.Faker, prefix string) string {
	timestamp := time.Now().UnixNano()
	randomPart := fake.UUID().V4()[:8]
	return fmt.Sprintf("%s_%d_%s", prefix, timestamp, randomPart)
}

// generateUniqueEmail generates a unique email with timestamp
func generateUniqueEmail(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	domain := fake.Internet().Domain()
	return fmt.Sprintf("%s_%d@%s", username, timestamp, domain)
}

// generateUniqueUsername generates a unique username with timestamp
func generateUniqueUsername(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	return fmt.Sprintf("%s_%d", username, timestamp)
}

// generateUniquePhone generates a unique phone number
func generateUniquePhone(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano() % 10000
	basePhone := fake.Phone().Number()
	// Remove non-digits and add timestamp suffix
	cleanPhone := ""
	for _, char := range basePhone {
		if char >= '0' && char <= '9' {
			cleanPhone += string(char)
		}
	}
	if len(cleanPhone) < 10 {
		cleanPhone = fmt.Sprintf("555%07d", timestamp)
	}
	return fmt.Sprintf("%s%d", cleanPhone[:min(len(cleanPhone), 6)], timestamp)
}

// generateUniqueSerialNumber generates a unique serial number
func generateUniqueSerialNumber(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	prefix := fake.Lorem().Text(3)
	return fmt.Sprintf("%s_%d", strings.ToUpper(prefix), timestamp)
}

// retryWithUniqueValues executes a function with retry logic for duplicate key errors
func retryWithUniqueValues[T any](maxRetries int, fn func(attempt int) (T, error)) (T, error) {
	var result T
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		result, err := fn(attempt)
		if err == nil {
			return result, nil
		}

		lastErr = err
		if !isDuplicateKeyError(err) {
			// Not a duplicate key error, don't retry
			return result, err
		}

		// Wait a bit before retrying to avoid rapid collisions
		time.Sleep(time.Millisecond * time.Duration(attempt+1))
	}

	return result, fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

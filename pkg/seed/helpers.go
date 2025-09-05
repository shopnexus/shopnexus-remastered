package seed

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jaswdr/faker/v2"
)

// UniqueTracker theo dõi các giá trị unique đã được tạo ra
type UniqueTracker struct {
	mu     sync.RWMutex
	values map[string]map[string]bool // type -> value -> exists
}

// NewUniqueTracker tạo một unique tracker mới
func NewUniqueTracker() *UniqueTracker {
	return &UniqueTracker{
		values: make(map[string]map[string]bool),
	}
}

// IsUnique kiểm tra xem giá trị có unique không
func (ut *UniqueTracker) IsUnique(valueType, value string) bool {
	ut.mu.RLock()
	defer ut.mu.RUnlock()
	
	if typeMap, exists := ut.values[valueType]; exists {
		return !typeMap[value]
	}
	return true
}

// Add thêm giá trị vào tracker
func (ut *UniqueTracker) Add(valueType, value string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	
	if ut.values[valueType] == nil {
		ut.values[valueType] = make(map[string]bool)
	}
	ut.values[valueType][value] = true
}

// Clear xóa tất cả giá trị trong tracker
func (ut *UniqueTracker) Clear() {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.values = make(map[string]map[string]bool)
}

// isDuplicateKeyError checks if the error is a duplicate key constraint violation
// DEPRECATED: This function is no longer needed since we use local uniqueness checking
// Kept for backward compatibility only
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

// generateUniqueCodeWithTracker generates a unique code với local checking
func generateUniqueCodeWithTracker(fake *faker.Faker, prefix string, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := prefix + "_CODE"
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		code := generateUniqueCode(fake, prefix)
		if tracker.IsUnique(valueType, code) {
			tracker.Add(valueType, code)
			return code
		}
		// Thêm thời gian chờ ngắn để tránh collision
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}
	
	// Fallback với timestamp chi tiết hơn nếu vẫn không unique
	timestamp := time.Now().UnixNano()
	randomPart := fake.UUID().V4()
	code := fmt.Sprintf("%s_%d_%s", prefix, timestamp, randomPart)
	tracker.Add(valueType, code)
	return code
}

// generateUniqueEmail generates a unique email with timestamp
func generateUniqueEmail(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	domain := fake.Internet().Domain()
	return fmt.Sprintf("%s_%d@%s", username, timestamp, domain)
}

// generateUniqueEmailWithTracker generates a unique email với local checking
func generateUniqueEmailWithTracker(fake *faker.Faker, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := "EMAIL"
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		email := generateUniqueEmail(fake)
		if tracker.IsUnique(valueType, email) {
			tracker.Add(valueType, email)
			return email
		}
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}
	
	// Fallback
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	domain := fake.Internet().Domain()
	randomPart := fake.UUID().V4()[:8]
	email := fmt.Sprintf("%s_%d_%s@%s", username, timestamp, randomPart, domain)
	tracker.Add(valueType, email)
	return email
}

// generateUniqueUsername generates a unique username with timestamp
func generateUniqueUsername(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	return fmt.Sprintf("%s_%d", username, timestamp)
}

// generateUniqueUsernameWithTracker generates a unique username với local checking
func generateUniqueUsernameWithTracker(fake *faker.Faker, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := "USERNAME"
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		username := generateUniqueUsername(fake)
		if tracker.IsUnique(valueType, username) {
			tracker.Add(valueType, username)
			return username
		}
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}
	
	// Fallback
	timestamp := time.Now().UnixNano()
	username := fake.Internet().User()
	randomPart := fake.UUID().V4()[:8]
	result := fmt.Sprintf("%s_%d_%s", username, timestamp, randomPart)
	tracker.Add(valueType, result)
	return result
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

// generateUniquePhoneWithTracker generates a unique phone với local checking
func generateUniquePhoneWithTracker(fake *faker.Faker, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := "PHONE"
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		phone := generateUniquePhone(fake)
		if tracker.IsUnique(valueType, phone) {
			tracker.Add(valueType, phone)
			return phone
		}
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}
	
	// Fallback
	timestamp := time.Now().UnixNano()
	phone := fmt.Sprintf("555%013d", timestamp%10000000000000)
	tracker.Add(valueType, phone)
	return phone
}

// generateUniqueSerialNumber generates a unique serial number
func generateUniqueSerialNumber(fake *faker.Faker) string {
	timestamp := time.Now().UnixNano()
	prefix := fake.Lorem().Text(3)
	return fmt.Sprintf("%s_%d", strings.ToUpper(prefix), timestamp)
}

// generateUniqueSerialNumberWithTracker generates a unique serial number với local checking
func generateUniqueSerialNumberWithTracker(fake *faker.Faker, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := "SERIAL_NUMBER"
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		serial := generateUniqueSerialNumber(fake)
		if tracker.IsUnique(valueType, serial) {
			tracker.Add(valueType, serial)
			return serial
		}
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}
	
	// Fallback
	timestamp := time.Now().UnixNano()
	prefix := fake.Lorem().Text(3)
	randomPart := fake.UUID().V4()[:8]
	serial := fmt.Sprintf("%s_%d_%s", strings.ToUpper(prefix), timestamp, randomPart)
	tracker.Add(valueType, serial)
	return serial
}

// retryWithUniqueValues executes a function with retry logic for duplicate key errors
// DEPRECATED: This function is no longer needed since we use local uniqueness checking
// which eliminates the need for database-level retry logic. Kept for backward compatibility.
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

// generateBulkUniqueValues generates multiple unique values at once to avoid collisions
// DEPRECATED: With local uniqueness checking, individual generation with tracker is preferred
// This is kept for backward compatibility
func generateBulkUniqueValues(count int, generator func(int) string) []string {
	values := make([]string, count)
	for i := 0; i < count; i++ {
		values[i] = generator(i)
	}
	return values
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

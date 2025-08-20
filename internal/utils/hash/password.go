package hash

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Hash hashes the given password
func Password(plain string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %s", err)
	}

	if err = bcrypt.CompareHashAndPassword(hashedPassword, []byte(plain)); err != nil {
		return "", fmt.Errorf("failed to compare password: %s", err)
	}

	return string(hashedPassword), nil
}

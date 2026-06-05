package accountmodel

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	jwt.RegisteredClaims

	Account AuthenticatedAccount
}

type AuthenticatedAccount struct {
	ID     uuid.UUID `validate:"required"`
	Number int64     `validate:"required"`
	Role   Role      `json:"role"`
}

// IsAdmin reports whether the authenticated account has platform-staff
// privileges (e.g. dispute resolution).
func (a AuthenticatedAccount) IsAdmin() bool {
	return a.Role == RoleAdmin
}

package authmodel

import (
	"shopnexus-remastered/internal/db"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	jwt.RegisteredClaims
	AccountID int64
	Type      db.AccountType
}

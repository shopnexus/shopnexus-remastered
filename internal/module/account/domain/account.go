// Package domain: account entity + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type Account struct {
	ID           int64
	Email        string `validate:"required,email"`
	PasswordHash string `validate:"required"`
	Name         string `validate:"required"`
	CreatedAt    time.Time
}

func NewAccount(email, name, passwordHash string) (Account, error) {
	a := Account{Email: email, Name: name, PasswordHash: passwordHash}
	if err := validation.Default().Struct(a); err != nil {
		return Account{}, errx.ErrValidation.Fmt(err.Error())
	}
	return a, nil
}

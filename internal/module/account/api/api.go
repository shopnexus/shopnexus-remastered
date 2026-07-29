// Package accountapi is the published contract of the account service.
// Only interface + DTO + validate tag (string). The sole non-stdlib import is
// shared/id, whose ID[K] carries the opaque-id conversion for every key field.
package accountapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type RegisterRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8,max=72"`
	Name     string `json:"name" validate:"required,max=80"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type GetProfileRequest struct {
	UserID id.ID[id.Account] `validate:"required"`
}

type Profile struct {
	ID          id.ID[id.Account] `json:"id"`
	DisplayName string            `json:"display_name"`
	Email       string            `json:"email"`
}

type Token struct {
	AccessToken string `json:"access_token"`
}

type Service interface {
	Register(ctx context.Context, req RegisterRequest) (Profile, error)
	Login(ctx context.Context, req LoginRequest) (Token, error)
	GetProfile(ctx context.Context, req GetProfileRequest) (Profile, error)
}

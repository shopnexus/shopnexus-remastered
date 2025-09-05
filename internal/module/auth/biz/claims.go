package authbiz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/client/cachestruct"
	authmodel "shopnexus-remastered/internal/module/auth/model"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	tokenHeader        = "authorization"
	tokenPrefix        = "Bearer "
	tokenCacheDuration = 5 * 60 * time.Second
)

var claimsCache = cachestruct.NewInMemoryClient()

// GetClaims retrieves and validates JWT claims from the token, using an in-memory cache
func GetClaims(r *http.Request) (authmodel.Claims, error) {
	token := r.Header.Get(tokenHeader)

	if token == "" {
		return authmodel.Claims{}, fmt.Errorf("missing authorization header")
	}

	// Try to get claims from cache first
	var claims authmodel.Claims
	if err := claimsCache.Get(context.Background(), token, &claims); err == nil {
		return claims, nil
	}

	// If not in cache, validate token and store in cache
	claims, err := ValidateAccessToken(strings.TrimPrefix(token, tokenPrefix))
	if err != nil {
		return authmodel.Claims{}, err
	}

	// Store claims in cache
	if err = claimsCache.Set(context.Background(), token, claims, tokenCacheDuration); err != nil {
		return authmodel.Claims{}, err
	}

	return claims, nil
}

func ValidateAccessToken(tokenStr string) (claims authmodel.Claims, err error) {
	secret := config.GetConfig().App.JWT.Secret
	token, err := jwt.ParseWithClaims(tokenStr, &claims, func(token *jwt.Token) (any, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return claims, err
	}

	if !token.Valid {
		return claims, errors.New("invalid token or token expired")
	}

	return claims, nil
}

package accountbiz

import (
	"context"
	"errors"
	"fmt"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	authclaims "shopnexus-server/internal/shared/claims"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/validator"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// CreateClaims generates JWT claims for the given account.
func (a *AccountHandler) CreateClaims(account accountdb.AccountAccount) accountmodel.Claims {
	return accountmodel.Claims{
		Account: accountmodel.AuthenticatedAccount{
			ID:     account.ID,
			Number: account.Number,
			Role:   accountmodel.Role(account.Role),
		},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   account.ID.String(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(a.tokenDuration)),
		},
	}
}

// GenerateAccessToken creates a JWT access token for the given account.
func (a *AccountHandler) GenerateAccessToken(account accountdb.AccountAccount) (string, error) {
	claims := a.CreateClaims(account)
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)

	signedToken, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("signed access token: %w", err)
	}

	return signedToken, nil
}

// CreateRefreshClaims generates JWT claims for a refresh token.
func (a *AccountHandler) CreateRefreshClaims(account accountdb.AccountAccount) accountmodel.Claims {
	return accountmodel.Claims{
		Account: accountmodel.AuthenticatedAccount{
			ID:     account.ID,
			Number: account.Number,
			Role:   accountmodel.Role(account.Role),
		},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   account.ID.String(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(a.refreshTokenDuration)),
		},
	}
}

// GenerateRefreshToken creates a signed JWT refresh token for the given account.
func (a *AccountHandler) GenerateRefreshToken(account accountdb.AccountAccount) (string, error) {
	claims := a.CreateRefreshClaims(account)
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	signedToken, err := token.SignedString(a.refreshSecret)
	if err != nil {
		return "", fmt.Errorf("signed refresh token: %w", err)
	}
	return signedToken, nil
}

// ComparePassword checks if the provided password matches the hashed password.
func (a *AccountHandler) ComparePassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// CreateHash generates a hashed password (currently using bcrypt).
func (a *AccountHandler) CreateHash(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashedPassword), nil
}

type LoginParams struct {
	Username null.String `validate:"omitnil"`
	Email    null.String `validate:"omitnil"`
	Phone    null.String `validate:"omitnil"`
	Password null.String `validate:"omitnil,min=8,max=72"`
}

type LoginResult struct {
	Account      accountdb.AccountAccount
	AccessToken  string
	RefreshToken string
}

// Login authenticates a user and returns access and refresh tokens.
func (a *AccountHandler) Login(ctx context.Context, params LoginParams) (LoginResult, error) {
	var zero LoginResult

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate login params: %w", err)
	}

	if !params.Username.Valid && !params.Email.Valid && !params.Phone.Valid {
		return zero, accountmodel.ErrMissingIdentifier
	}

	account, err := a.storage.Querier().GetAccount(ctx, accountdb.GetAccountParams{
		Phone:    params.Phone,
		Email:    params.Email,
		Username: params.Username,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, accountmodel.ErrAccountNotFound
		}
		return zero, fmt.Errorf("db get account: %w", err)
	}

	// If the account has a password, require it for login
	if account.Password.Valid {
		if !params.Password.Valid {
			return zero, accountmodel.ErrInvalidCredentials
		}
		if !a.ComparePassword(account.Password.String, params.Password.String) {
			return zero, accountmodel.ErrInvalidCredentials
		}
	}

	accessToken, err := a.GenerateAccessToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := a.GenerateRefreshToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate refresh token: %w", err)
	}

	return LoginResult{
		Account:      account,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

type RegisterParams struct {
	Username null.String `validate:"omitnil,min=1,max=255"`
	Email    null.String `validate:"omitnil,email"`
	Phone    null.String `validate:"omitnil,e164"`
	Password null.String `validate:"required,min=8,max=72"`
	Country  string      `validate:"required,len=2,uppercase,alpha"`
}

type RegisterResult struct {
	Account      accountdb.AccountAccount
	AccessToken  string
	RefreshToken string
}

// Register creates a new account with the given credentials and returns tokens.
func (a *AccountHandler) Register(ctx context.Context, params RegisterParams) (RegisterResult, error) {
	var zero RegisterResult

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate register params: %w", err)
	}

	if !params.Username.Valid && !params.Email.Valid && !params.Phone.Valid {
		return zero, accountmodel.ErrMissingIdentifier
	}

	// If register via Google OAuth, password can be nil => password is nil, email is required
	//! More oauth providers can be added in the future
	if !params.Password.Valid && !params.Email.Valid {
		return zero, accountmodel.ErrEmailRequiredForOAuth
	}

	// Validate country maps to a real currency before any DB work.
	if _, err := sharedcurrency.Infer(params.Country); err != nil {
		return zero, accountmodel.ErrInvalidCountry.Fmt(err)
	}

	// Hash the password if provided
	var hashedPassword null.String
	if params.Password.Valid {
		hashed, err := a.CreateHash(params.Password.String)
		if err != nil {
			return zero, fmt.Errorf("create hash: %w", err)
		}
		hashedPassword.SetValid(hashed)
	}

	// Create account base
	account, err := a.storage.Querier().CreateDefaultAccount(ctx, accountdb.CreateDefaultAccountParams{
		Phone:    params.Phone,
		Email:    params.Email,
		Username: params.Username,
		Password: hashedPassword,
	})
	if err != nil {
		return zero, fmt.Errorf("db create account: %w", err)
	}

	// Create profile with the submitted country. Currency is inferred from
	// country at read time in mapProfile, so nothing to persist here.
	if _, err := a.storage.Querier().CreateSignupProfile(ctx, accountdb.CreateSignupProfileParams{
		ID:      account.ID,
		Country: params.Country,
	}); err != nil {
		return zero, fmt.Errorf("db create profile: %w", err)
	}

	accessToken, err := a.GenerateAccessToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := a.GenerateRefreshToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate refresh token: %w", err)
	}

	// Welcome notification
	if err = a.self.Guaranteed().Send().CreateNotification(ctx, CreateNotificationParams{
		AccountID: account.ID,
		Type:      accountmodel.NotiWelcome,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Welcome to ShopNexus",
		Content:   "Your account has been created successfully. Start exploring!",
	}); err != nil {
		return zero, fmt.Errorf("send welcome notification: %w", err)
	}

	return RegisterResult{
		Account:      account,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

type RefreshResult struct {
	AccessToken  string
	RefreshToken string
}

// Refresh validates a refresh token and issues new access and refresh tokens.
func (a *AccountHandler) Refresh(ctx context.Context, refreshToken string) (RefreshResult, error) {
	var zero RefreshResult
	claims, err := authclaims.ValidateAccessToken(string(a.refreshSecret), refreshToken)
	if err != nil {
		return zero, fmt.Errorf("validate refresh token: %w", err)
	}

	account, err := a.storage.Querier().GetAccount(ctx, accountdb.GetAccountParams{
		ID: uuid.NullUUID{UUID: claims.Account.ID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("db get account: %w", err)
	}

	access, err := a.GenerateAccessToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate access token: %w", err)
	}
	nextRefresh, err := a.GenerateRefreshToken(account)
	if err != nil {
		return zero, fmt.Errorf("generate refresh token: %w", err)
	}

	return RefreshResult{
		AccessToken:  access,
		RefreshToken: nextRefresh,
	}, nil
}

package authbiz

import (
	"fmt"
	"strconv"
	"time"

	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/db"
	accountbiz "shopnexus-remastered/internal/module/account/biz"
	authmodel "shopnexus-remastered/internal/module/auth/model"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/context"
)

type AuthBiz struct {
	tokenDuration time.Duration
	jwtSecret     []byte

	accountBiz *accountbiz.AccountBiz
}

func NewAuthBiz(accountBiz *accountbiz.AccountBiz) *AuthBiz {
	return &AuthBiz{
		tokenDuration: time.Duration(config.GetConfig().App.JWT.AccessTokenDuration * int64(time.Second)),
		jwtSecret:     []byte(config.GetConfig().App.JWT.Secret),
		accountBiz:    accountBiz,
	}
}

// CreateClaims generates JWT claims for the given account.
func (a *AuthBiz) CreateClaims(account db.AccountAccount) authmodel.Claims {
	return authmodel.Claims{
		AccountID: account.ID,
		Type:      account.Type,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "shopnexus",
			Subject:   strconv.Itoa(int(account.ID)),
			Audience:  []string{"shopnexus"},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(a.tokenDuration)),
		},
	}
}

// GenerateAccessToken creates a JWT access token for the given account.
func (a *AuthBiz) GenerateAccessToken(account db.AccountAccount) (string, error) {
	claims := a.CreateClaims(account)
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)

	signedToken, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

// ComparePassword checks if the provided password matches the hashed password.
func (a *AuthBiz) ComparePassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// CreateHash generates a hashed password (currently using bcrypt).
func (a *AuthBiz) CreateHash(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedPassword), nil
}

type LoginParams struct {
	Code     *string
	Username *string
	Email    *string
	Phone    *string
	Password *string
}

type LoginResult struct {
	Account     db.AccountAccount
	AccessToken string
}

func (a *AuthBiz) Login(ctx context.Context, params LoginParams) (LoginResult, error) {
	var zero LoginResult

	account, err := a.accountBiz.Find(ctx, accountbiz.FindParams{
		Code:     params.Code,
		Username: params.Username,
		Email:    params.Email,
		Phone:    params.Phone,
	})
	if err != nil {
		return zero, err
	}

	if account.Password.Valid && params.Password != nil {
		if !a.ComparePassword(account.Password.String, *params.Password) {
			return zero, authmodel.ErrInvalidCredentials
		}
	}

	accessToken, err := a.GenerateAccessToken(account)
	if err != nil {
		return zero, err
	}

	return LoginResult{
		Account:     account,
		AccessToken: accessToken,
	}, nil
}

type RegisterParams struct {
	Type     db.AccountType
	Username *string
	Email    *string
	Phone    *string
	Password *string
}

type RegisterResult struct {
	Account     db.AccountAccount
	AccessToken string
}

func (a *AuthBiz) Register(ctx context.Context, params RegisterParams) (RegisterResult, error) {
	var zero RegisterResult

	if params.Username == nil && params.Email == nil && params.Phone == nil {
		return zero, authmodel.ErrMissingIdentifier
	}

	// If register via Google OAuth, password can be nil => password is nil, email is required
	//! More oauth providers can be added in the future
	if params.Password == nil && params.Email == nil {
		return zero, fmt.Errorf("email is required when password is not provided")
	}

	// Hash the password if provided
	var hashedPassword *string
	if params.Password != nil {
		hashed, err := a.CreateHash(*params.Password)
		if err != nil {
			return zero, err
		}
		hashedPassword = &hashed
	}

	account, err := a.accountBiz.Create(ctx, accountbiz.CreateParams{
		Type:     params.Type,
		Username: params.Username,
		Email:    params.Email,
		Phone:    params.Phone,
		Password: hashedPassword,
	})
	if err != nil {
		return zero, err
	}

	accessToken, err := a.GenerateAccessToken(account)
	if err != nil {
		return zero, err
	}

	return RegisterResult{
		Account:     account,
		AccessToken: accessToken,
	}, nil
}

package accountbiz

import (
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

func NewAccountBiz(accountBiz *accountbiz.AccountBiz) *AuthBiz {
	return &AuthBiz{
		tokenDuration: time.Duration(config.GetConfig().App.JWT.AccessTokenDuration * int64(time.Second)),
		jwtSecret:     []byte(config.GetConfig().App.JWT.Secret),
		accountBiz:    accountBiz,
	}
}

// CreateClaims generates JWT claims for the given account.
func (a *AuthBiz) CreateClaims(account db.AccountBase) authmodel.Claims {
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
func (a *AuthBiz) GenerateAccessToken(account db.AccountBase) (string, error) {
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
	Type     db.AccountType
	UserID   *int64
	Username *string
	Email    *string
	Phone    *string
	Password string
}

func (a *AuthBiz) Login(ctx context.Context, params LoginParams) (db.AccountBase, error) {
	account, err := a.accountBiz.Find(ctx, accountbiz.FindParams{
		Type:     params.Type,
		UserID:   params.UserID,
		Username: params.Username,
		Email:    params.Email,
		Phone:    params.Phone,
	})
	if err != nil {
		return db.AccountBase{}, err
	}

	if a.ComparePassword(account.Password, params.Password) {
		return db.AccountBase{}, authmodel.ErrInvalidCredentials
	}

	return account, nil
}

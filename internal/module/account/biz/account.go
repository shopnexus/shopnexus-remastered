package accountbiz

import (
	"context"
	"errors"
	"fmt"

	"shopnexus-remastered/internal/db"
	authmodel "shopnexus-remastered/internal/module/auth/model"
	pgxptr "shopnexus-remastered/internal/utils/pgx/ptr"
	pgxsqlc "shopnexus-remastered/internal/utils/pgx/sqlc"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AccountBiz struct {
	storage *pgxsqlc.Storage
}

// NewAccountBiz creates a new instance of AccountBiz.
func NewAccountBiz(storage *pgxsqlc.Storage) *AccountBiz {
	return &AccountBiz{
		storage: storage,
	}
}

type FindParams struct {
	Code     *string
	Username *string
	Email    *string
	Phone    *string
}

func (s *AccountBiz) Find(ctx context.Context, params FindParams) (db.AccountAccount, error) {
	if params.Code == nil && params.Username == nil && params.Email == nil && params.Phone == nil {
		return db.AccountAccount{}, fmt.Errorf("at least one of username, email, or phone must be provided")
	}

	account, err := s.storage.GetAccount(ctx, db.GetAccountParams{
		Code:     ptr.DerefDefault(params.Code, ""),
		Username: *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Username),
		Email:    *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Email),
		Phone:    *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Phone),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AccountAccount{}, authmodel.ErrAccountNotFound
		}
		return account, err
	}

	return account, nil
}

type CreateParams struct {
	Type     db.AccountType
	Username *string
	Phone    *string
	Email    *string
	Password *string
}

func (s *AccountBiz) Create(ctx context.Context, params CreateParams) (db.AccountAccount, error) {
	var zero db.AccountAccount

	code := uuid.New().String()
	createdAccount, err := s.storage.CreateAccount(ctx, db.CreateAccountParams{
		Code:     code,
		Type:     params.Type,
		Status:   db.AccountStatusACTIVE,
		Phone:    *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Phone),
		Email:    *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Email),
		Username: *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Username),
		Password: *pgxptr.PtrToPgtype(&pgtype.Text{}, params.Password),
	})
	if err != nil {
		return zero, err
	}

	return createdAccount, nil
}

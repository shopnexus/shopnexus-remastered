package accountbiz

import (
	"context"
	"errors"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"

	"shopnexus-remastered/internal/db"
	authmodel "shopnexus-remastered/internal/module/auth/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AccountBiz struct {
	storage *pgutil.Storage
}

// NewAccountBiz creates a new instance of AccountBiz.
func NewAccountBiz(storage *pgutil.Storage) *AccountBiz {
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

func (s *AccountBiz) Find(ctx context.Context, params FindParams) (db.AccountBase, error) {
	if params.Code == nil && params.Username == nil && params.Email == nil && params.Phone == nil {
		return db.AccountBase{}, fmt.Errorf("at least one of username, email, or phone must be provided")
	}

	account, err := s.storage.GetAccountBase(ctx, db.GetAccountBaseParams{
		Code:     pgutil.PtrToPgtype(params.Code, pgutil.StringToPgText),
		Username: pgutil.PtrToPgtype(params.Username, pgutil.StringToPgText),
		Email:    pgutil.PtrToPgtype(params.Email, pgutil.StringToPgText),
		Phone:    pgutil.PtrToPgtype(params.Phone, pgutil.StringToPgText),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AccountBase{}, authmodel.ErrAccountNotFound
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

func (s *AccountBiz) Create(ctx context.Context, params CreateParams) error {
	code := uuid.New().String()
	_, err := s.storage.CreateDefaultAccountBase(ctx, []db.CreateDefaultAccountBaseParams{{
		Code:     code,
		Type:     params.Type,
		Phone:    pgutil.PtrToPgtype(params.Phone, pgutil.StringToPgText),
		Email:    pgutil.PtrToPgtype(params.Email, pgutil.StringToPgText),
		Username: pgutil.PtrToPgtype(params.Username, pgutil.StringToPgText),
		Password: pgutil.PtrToPgtype(params.Password, pgutil.StringToPgText),
	}})
	if err != nil {
		return err
	}

	return nil
}

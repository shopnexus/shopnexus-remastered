package accountbiz

import (
	"context"
	"fmt"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
)

type AccountBiz struct {
	storage db.Querier
}

// NewAccountBiz creates a new instance of AccountBiz.
func NewAccountBiz(storage db.Querier) *AccountBiz {
	return &AccountBiz{
		storage: storage,
	}
}

type FindParams struct {
	Type     db.AccountType
	UserID   *int64
	Username *string
	Email    *string
	Phone    *string
}

func (s *AccountBiz) Find(ctx context.Context, params FindParams) (account db.AccountBase, err error) {
	if params.Username == nil && params.Email == nil && params.Phone == nil && params.UserID == nil {
		return db.AccountBase{}, fmt.Errorf("at least one of Username, Email, Phone, or UserID must be provided")
	}

	switch params.Type {
	case db.AccountTypeAdmin:
		row, err := s.storage.GetAccountAdmin(ctx, db.GetAccountAdminParams{
			ID:       pgtype.Int8{Int64: ptr.DerefDefault(params.UserID, 0), Valid: params.UserID != nil},
			Username: pgtype.Text{String: ptr.DerefDefault(params.Username, ""), Valid: params.Username != nil},
		})
		if err != nil {
			return db.AccountBase{}, fmt.Errorf("failed to find admin account: %w", err)
		}
		account = db.AccountBase{
			ID:       row.ID,
			Username: row.Username,
			Type:     db.AccountTypeAdmin,
		}
	case db.AccountTypeUser:
		row, err := s.storage.GetAccountUser(ctx, db.GetAccountUserParams{
			ID:       pgtype.Int8{Int64: ptr.DerefDefault(params.UserID, 0), Valid: params.UserID != nil},
			Username: pgtype.Text{String: ptr.DerefDefault(params.Username, ""), Valid: params.Username != nil},
			Email:    pgtype.Text{String: ptr.DerefDefault(params.Email, ""), Valid: params.Email != nil},
			Phone:    pgtype.Text{String: ptr.DerefDefault(params.Phone, ""), Valid: params.Phone != nil},
		})
		if err != nil {
			return db.AccountBase{}, fmt.Errorf("failed to find user account: %w", err)
		}
		account = db.AccountBase{
			ID:       row.ID,
			Username: row.Username,
			Type:     db.AccountTypeUser,
		}
	default:
		return db.AccountBase{}, fmt.Errorf("unsupported account type: %s", params.Type)
	}

	return account, nil
}

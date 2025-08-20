package pgxsqlc

import (
	"context"

	"botmediasaver/generated/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)

	Begin(context.Context) (pgx.Tx, error)
}

type Storage struct {
	db DBTX
	*sqlc.Queries
}

func (s *Storage) BeginTx(ctx context.Context) (*TxStorage, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return &TxStorage{tx: tx, Queries: s.Queries.WithTx(tx)}, nil
}

type TxStorage struct {
	tx pgx.Tx
	*sqlc.Queries
}

func (s *TxStorage) Commit(ctx context.Context) error {
	return s.tx.Commit(ctx)
}

func (s *TxStorage) Rollback(ctx context.Context) error {
	return s.tx.Rollback(ctx)
}

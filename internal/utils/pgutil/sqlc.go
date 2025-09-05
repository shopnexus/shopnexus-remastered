package pgutil

import (
	"context"

	"shopnexus-remastered/internal/db"

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

func NewStorage(dbtx DBTX) *Storage {
	return &Storage{
		dbtx:    dbtx,
		Queries: db.New(dbtx),
	}
}

type Storage struct {
	dbtx DBTX
	*db.Queries
}

func (s *Storage) BeginTx(ctx context.Context) (*TxStorage, error) {
	tx, err := s.dbtx.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return &TxStorage{tx: tx, Queries: s.Queries.WithTx(tx)}, nil
}

type TxStorage struct {
	tx pgx.Tx
	*db.Queries
}

func (s *TxStorage) Commit(ctx context.Context) error {
	return s.tx.Commit(ctx)
}

func (s *TxStorage) Rollback(ctx context.Context) error {
	return s.tx.Rollback(ctx)
}

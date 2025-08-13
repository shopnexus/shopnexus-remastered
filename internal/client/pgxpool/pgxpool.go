package pgxpool

import (
	"botmediasaver/internal/logger"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PgxpoolOptions struct {
	Url             string `yaml:"url"`
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	Username        string `yaml:"username"`
	Password        string `yaml:"password"`
	Database        string `yaml:"database"`
	MaxConnections  int32  `yaml:"maxConnections"`
	MaxConnIdleTime int32  `yaml:"maxConnIdleTime"`
}

func NewPgxpool(opts PgxpoolOptions) (*pgxpool.Pool, error) {
	connStr := GetConnStr(opts)

	connConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, err
	}

	// Set maximum number of connections
	connConfig.MaxConns = opts.MaxConnections
	connConfig.ConnConfig.OnNotice = func(conn *pgconn.PgConn, notice *pgconn.Notice) {
		logger.Log.Info("notice", zap.String("message", notice.Message))
	}

	return pgxpool.NewWithConfig(context.Background(), connConfig)
}

func GetConnStr(opts PgxpoolOptions) string {
	if opts.Url == "" {
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			opts.Host,
			opts.Port,
			opts.Username,
			opts.Password,
			opts.Database,
		)
	}

	return opts.Url
}

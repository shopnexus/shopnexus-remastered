package pg

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

type Options struct {
	Url             string        `yaml:"url"`
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Username        string        `yaml:"username"`
	Password        string        `yaml:"password"`
	Database        string        `yaml:"database"`
	MaxConnections  int32         `yaml:"maxConnections"`
	MaxConnIdleTime time.Duration `yaml:"maxConnIdleTime"`
}

func New(opts Options) (*pgxpool.Pool, error) {
	connStr := GetConnStr(opts)

	connConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, err
	}

	// Set maximum number of connections
	connConfig.MaxConns = opts.MaxConnections
	connConfig.MaxConnIdleTime = opts.MaxConnIdleTime
	connConfig.ConnConfig.OnNotice = func(conn *pgconn.PgConn, notice *pgconn.Notice) {
		slog.Warn("PostgreSQL notice: " + notice.Message)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), connConfig)
	if err != nil {
		return nil, err
	}

	// Collect the custom data types once, store them in memory, and register them for every future connection.
	customTypes, err := getCustomDataTypes(context.Background(), pool)
	if err != nil {
		return nil, err
	}
	connConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if err := pgxvector.RegisterTypes(ctx, conn); err != nil {
			return fmt.Errorf("register pgvector types: %w", err)
		}
		for _, t := range customTypes {
			conn.TypeMap().RegisterType(t)
		}
		return nil
	}

	// Immediately close the old pool and open a new one with the new config.
	pool.Close()
	return pgxpool.NewWithConfig(context.Background(), connConfig)
}

// Any custom DB types made with CREATE TYPE need to be registered with pgx.
// https://github.com/kyleconroy/sqlc/issues/2116
// https://stackoverflow.com/questions/75658429/need-to-update-psql-row-of-a-composite-type-in-golang-with-jack-pgx
// https://pkg.go.dev/github.com/jackc/pgx/v5/pgtype
func getCustomDataTypes(ctx context.Context, pool *pgxpool.Pool) ([]*pgtype.Type, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Find all custom types in your schemas
	query := `
	SELECT n.nspname || '.' || t.typname AS type_name
	FROM pg_type t
	JOIN pg_namespace n ON t.typnamespace = n.oid
	WHERE n.nspname !~ '^pg_'
	AND n.nspname != 'information_schema'
	AND t.typtype IN ('e')
	ORDER BY type_name;
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	// First, collect all type names while the rows are open
	var typeNames []string
	for rows.Next() {
		var typeName string // Eg: promotion.discount
		if err := rows.Scan(&typeName); err != nil {
			continue
		}
		typeNames = append(typeNames, typeName)
	}
	rows.Close() // Close rows to release the connection

	// Now iterate over collected type names and load/register them
	var typesToRegister []*pgtype.Type
	for _, typeName := range typeNames {
		// Split typeName (e.g., "promotion.discount") into schema and type
		parts := strings.Split(typeName, ".")
		if len(parts) != 2 {
			continue // Skip if format is unexpected
		}
		schema := parts[0]
		typeNameOnly := parts[1]

		// Format as "schema"."type" (e.g., "promotion"."discount")
		quotedTypeName := fmt.Sprintf(`"%s"."%s"`, schema, typeNameOnly)
		// Format array type as "schema"."_type" (e.g., "promotion"."_discount")
		quotedArrayTypeName := fmt.Sprintf(`"%s"."_%s"`, schema, typeNameOnly)

		fmt.Println("Registering custom type:", quotedTypeName, "and array type:", quotedArrayTypeName)

		// Load and register the base type
		dataType, err := conn.Conn().LoadType(ctx, quotedTypeName)
		if err != nil {
			slog.Warn("Failed to load type for " + quotedTypeName + ": " + err.Error())
			continue
		}
		conn.Conn().TypeMap().RegisterType(dataType)
		typesToRegister = append(typesToRegister, dataType)

		// Load and register the array type
		arrayType, err := conn.Conn().LoadType(ctx, quotedArrayTypeName)
		if err != nil {
			slog.Warn("Failed to load array type for " + quotedArrayTypeName + ": " + err.Error())
			continue
		}
		conn.Conn().TypeMap().RegisterType(arrayType)
		typesToRegister = append(typesToRegister, arrayType)
	}

	return typesToRegister, nil
}

func GetConnStr(opts Options) string {
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

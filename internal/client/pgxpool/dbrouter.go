package pgxpool

import (
	"context"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBRouter wrapper that routes queries to appropriate pools based on operation type
type DBRouter struct {
	pgpoolread  DBTX
	pgpoolwrite DBTX
}

// NewDBRouter creates a new DBRouter database wrapper
func NewDBRouter(readPool, writePool DBTX) *DBRouter {
	return &DBRouter{
		pgpoolread:  readPool,
		pgpoolwrite: writePool,
	}
}

// isWriteOperation determines if the SQL query is a write operation
func (c *DBRouter) isWriteOperation(query string) bool {
	// Normalize the query by trimming whitespace and converting to lowercase
	normalized := strings.TrimSpace(strings.ToLower(query))

	// Remove single-line comments (-- comments) - handle multiline properly
	lines := strings.Split(normalized, "\n")
	var cleanLines []string
	for _, line := range lines {
		// Remove -- comments from each line
		if idx := strings.Index(line, "--"); idx != -1 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}
	normalized = strings.Join(cleanLines, " ")

	// Remove block comments (/* ... */)
	blockCommentRegex := regexp.MustCompile(`/\*.*?\*/`)
	normalized = blockCommentRegex.ReplaceAllString(normalized, "")
	normalized = strings.TrimSpace(normalized)

	// Check for write operations
	writePatterns := []string{
		"insert",
		"update",
		"delete",
		"create",
		"drop",
		"alter",
		"truncate",
		"replace",
		"merge",
		"upsert",
		"call", // stored procedures might modify data
		"exec", // execute statements might modify data
	}

	for _, pattern := range writePatterns {
		if strings.HasPrefix(normalized, pattern) {
			return true
		}
	}

	// Special cases for WITH clauses that contain write operations
	if strings.HasPrefix(normalized, "with") {
		withWriteRegex := regexp.MustCompile(`\b(insert|update|delete|create|drop|alter|truncate|replace|merge|upsert)\b`)
		if withWriteRegex.MatchString(normalized) {
			return true
		}
	}

	return false
}

// selectPool returns the appropriate pool based on query type
func (c *DBRouter) selectPool(query string) DBTX {
	if c.isWriteOperation(query) {
		return c.pgpoolwrite
	}
	return c.pgpoolread
}

// Exec executes a query without returning any rows
func (c *DBRouter) Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error) {
	pool := c.selectPool(sql)
	return pool.Exec(ctx, sql, arguments...)
}

// Query executes a query that returns rows
func (c *DBRouter) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	pool := c.selectPool(sql)
	return pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that is expected to return at most one row
func (c *DBRouter) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	pool := c.selectPool(sql)
	return pool.QueryRow(ctx, sql, args...)
}

// CopyFrom uses COPY protocol to insert multiple rows
// This is always a write operation, so it uses the write pool
func (c *DBRouter) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return c.pgpoolwrite.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

// Begin starts a transaction
// Transactions can contain both read and write operations, so we use the write pool
// to ensure consistency and avoid potential issues with read replicas
func (c *DBRouter) Begin(ctx context.Context) (pgx.Tx, error) {
	return c.pgpoolwrite.Begin(ctx)
}

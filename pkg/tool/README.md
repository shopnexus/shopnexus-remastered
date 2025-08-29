# SQLC Query Generator

A Go CLI tool that automatically generates SQLC queries from SQL schema files. This tool parses your database migration files and creates standardized CRUD operations following the patterns used in your existing codebase.

## Features

- **Schema Parsing**: Automatically parses PostgreSQL schema files with support for:
  - Multiple schemas
  - Enum types
  - Primary keys
  - Nullable columns
  - Default values
  - Serial columns

- **Template-Based Generation**: Uses customizable templates for:
  - `Get{Table}` - Single record retrieval
  - `List{Table}s` - Paginated listing with filters
  - `Create{Table}` - Record insertion
  - `Update{Table}` - Record updates with nullable fields
  - `Delete{Table}` - Record deletion

- **Flexible Output**: Generate queries for all tables or specific tables

## Installation

```bash
# Run directly from the tool directory
cd tool
go run . -help

# Or build the binary
go build -o sqlc-generator .
```

## Usage

### Basic Usage

```bash
# Generate queries for all tables
go run tool/main.go -schema prisma/migrations/0_init/migration.sql

# Generate queries for a specific table (using schema.table format)
go run tool/main.go -schema prisma/migrations/0_init/migration.sql -table account.account

# Custom output directory
go run tool/main.go -schema prisma/migrations/0_init/migration.sql -output generated_queries

# Generate all queries into a single file
go run tool/main.go -schema prisma/migrations/0_init/migration.sql -single-file
```

### Command Line Options

- `-schema <file>` - Path to SQL migration file (required)
- `-output <dir>` - Output directory for generated SQL files (default: queries)
- `-table <name>` - Generate queries for specific table (format: schema.table or just table)
- `-templates <dir>` - Directory containing template files (default: tool/templates)
- `-single-file` - Generate all queries into a single file (only when table not specified)
- `-help` - Show help message

## Template Customization

The tool creates default templates in `tool/templates/` directory. You can customize these templates to match your specific requirements:

- `get.sql.tmpl` - Single record queries
- `list.sql.tmpl` - List/search queries with filters
- `create.sql.tmpl` - Insert queries
- `update.sql.tmpl` - Update queries
- `delete.sql.tmpl` - Delete queries

### Template Variables

Templates have access to the following variables and functions:

**Table Object:**

- `.Schema` - Schema name
- `.Name` - Table name
- `.Columns` - All columns
- `.GetFullTableName()` - Quoted schema.table name (for SQL)
- `.GetQualifiedName()` - schema.table format
- `.GetSafeFileName()` - schema_table format (for filenames)
- `.GetNonSerialColumns()` - Columns excluding SERIAL types
- `.GetUpdatableColumns()` - Columns excluding SERIAL and primary keys
- `.GetFilterableColumns()` - Columns suitable for filtering
- `.GetPrimaryKeyColumns()` - Primary key columns
- `.HasDateColumns()` - Whether table has date_* columns

**Template Functions:**

- `{{.Name | pascalCase}}` - Convert to PascalCase
- `{{.Name | camelCase}}` - Convert to camelCase
- `{{sqlcArg "name"}}` - Generate sqlc.arg('name')
- `{{sqlcNarg "name"}}` - Generate sqlc.narg('name')
- `{{increment $i}}` - Add 1 to index

## Example Output

For a table `account.account`, the tool generates:

```sql
-- name: GetAccount :one
SELECT *
FROM "account"."account"
WHERE id = $1;

-- name: ListAccounts :many
SELECT *
FROM "account"."account"
WHERE (
    (code = sqlc.narg('code') OR sqlc.narg('code') IS NULL) AND
    (type = sqlc.narg('type') OR sqlc.narg('type') IS NULL) AND
    (status = sqlc.narg('status') OR sqlc.narg('status') IS NULL) AND
    (phone = sqlc.narg('phone') OR sqlc.narg('phone') IS NULL) AND
    (email = sqlc.narg('email') OR sqlc.narg('email') IS NULL) AND
    (username = sqlc.narg('username') OR sqlc.narg('username') IS NULL) AND
    (date_created >= sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL) AND
    (date_created <= sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL) AND
    (date_updated >= sqlc.narg('date_updated_from') OR sqlc.narg('date_updated_from') IS NULL) AND
    (date_updated <= sqlc.narg('date_updated_to') OR sqlc.narg('date_updated_to') IS NULL)
)
ORDER BY date_created DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateAccount :one
INSERT INTO "account"."account" (code, type, status, phone, email, username, password, date_created, date_updated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: UpdateAccount :one
UPDATE "account"."account"
SET code = COALESCE(sqlc.narg('code'), code),
    type = COALESCE(sqlc.narg('type'), type),
    status = COALESCE(sqlc.narg('status'), status),
    phone = COALESCE(sqlc.narg('phone'), phone),
    email = COALESCE(sqlc.narg('email'), email),
    username = COALESCE(sqlc.narg('username'), username),
    password = COALESCE(sqlc.narg('password'), password),
    date_created = COALESCE(sqlc.narg('date_created'), date_created),
    date_updated = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: DeleteAccount :exec
DELETE FROM "account"."account"
WHERE id = $1;
```

## Integration with SQLC

After generating the queries, run SQLC to generate the Go code:

```bash
sqlc generate
```

The tool is designed to work with your existing SQLC configuration and follows the same patterns as your current queries.

## Troubleshooting

**Template not found errors**: The tool creates default templates automatically. If you see template errors, check that the `tool/templates/` directory exists and contains `.sql.tmpl` files.

**Schema parsing errors**: Ensure your migration file follows standard PostgreSQL syntax. The parser handles most common CREATE TABLE patterns.

**Generated query syntax errors**: Check the templates for any syntax issues. You can customize templates to match your specific SQL requirements.

**Multi-schema support**: The tool generates files with `schema_table.sql` naming to avoid conflicts. For example, `account.account` table generates `account_account.sql`.

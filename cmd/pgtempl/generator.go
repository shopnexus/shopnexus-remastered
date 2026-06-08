package main

import (
	"fmt"
	"strings"
)

// Generator builds SQLC query strings for a given table.
type Generator struct {
	IncludeSchema bool // prefix query names with schema in PascalCase
}

// Generate returns all queries for one table, joined with "\n\n".
func (g *Generator) Generate(table *Table) string {
	// Key-only tables (only PK columns) are dedup ledgers (idempotency keys,
	// processed-event sets). Generic CRUD is meaningless for them; rely on
	// hand-written queries to define the operations.
	if len(table.UpdatableColumns()) == 0 {
		return ""
	}

	// Vector tables (pgvector embeddings) are only ever upserted/ANN-searched
	// via hand-written queries; generic CRUD is unused and would pull
	// vector/sparsevec into generated structs. Skip them.
	if table.HasVectorColumn() {
		return ""
	}

	var queries []string

	queries = append(queries, g.generateGet(table))
	queries = append(queries, g.generateCount(table))
	// List / ListCount are no longer SQL templates — the repo layer (-emit repo)
	// owns listing via repolist, scanning with pgx. Get/Count/Create*/Update/Delete stay.
	queries = append(queries, g.generateCreate(table))
	queries = append(queries, g.generateCreateBatch(table))
	queries = append(queries, g.generateCreateCopy(table))
	queries = append(queries, g.generateCreateDefault(table))
	queries = append(queries, g.generateCreateCopyDefault(table))
	if upd := g.generateUpdate(table); upd != "" {
		queries = append(queries, upd)
	}
	queries = append(queries, g.generateDelete(table))

	return strings.Join(queries, "\n\n")
}

// queryName builds the SQLC query name with optional schema prefix.
func (g *Generator) queryName(prefix string, table *Table) string {
	if g.IncludeSchema {
		return prefix + toPascalCase(table.Schema) + toPascalCase(table.Name)
	}
	return prefix + toPascalCase(table.Name)
}

// toPascalCase converts snake_case to PascalCase.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// filterWhereClause builds the shared WHERE clause used by Count, List, ListCount, and Delete.
func filterWhereClause(table *Table) string {
	cols := table.FilterableColumns()
	var conditions []string

	for _, col := range cols {
		// Always add the ANY condition.
		conditions = append(conditions,
			fmt.Sprintf(`(%s = ANY(sqlc.slice('%s')) OR sqlc.slice('%s') IS NULL)`, col.Quoted(), col.Name, col.Name))

		// Add range conditions if applicable.
		if col.IsRangeFilterable() {
			conditions = append(
				conditions,
				fmt.Sprintf(
					`(%s >= sqlc.narg('%s_from') OR sqlc.narg('%s_from') IS NULL)`,
					col.Quoted(),
					col.Name,
					col.Name,
				),
			)
			conditions = append(
				conditions,
				fmt.Sprintf(
					`(%s <= sqlc.narg('%s_to') OR sqlc.narg('%s_to') IS NULL)`,
					col.Quoted(),
					col.Name,
					col.Name,
				),
			)
		}
	}

	return "WHERE (\n    " + strings.Join(conditions, " AND\n    ") + "\n)"
}

// generateGet builds the GET query.
func (g *Generator) generateGet(table *Table) string {
	name := g.queryName("Get", table)

	var whereClause string
	idSets := table.IdentifierSets()
	if len(idSets) == 0 {
		whereClause = `WHERE "id" = $1`
	} else {
		var groups []string
		for _, set := range idSets {
			if len(set) == 1 {
				groups = append(groups,
					fmt.Sprintf(`(%s = sqlc.narg('%s'))`, set[0].Quoted(), set[0].Name))
			} else {
				var parts []string
				for _, col := range set {
					parts = append(parts,
						fmt.Sprintf(`%s = sqlc.narg('%s')`, col.Quoted(), col.Name))
				}
				groups = append(groups, "("+strings.Join(parts, " AND ")+")")
			}
		}
		whereClause = "WHERE " + strings.Join(groups, " OR ")
	}

	return fmt.Sprintf("-- name: %s :one\nSELECT *\nFROM %s\n%s;", name, table.FullName(), whereClause)
}

// generateCount builds the COUNT query.
func (g *Generator) generateCount(table *Table) string {
	name := g.queryName("Count", table)
	where := filterWhereClause(table)

	return fmt.Sprintf("-- name: %s :one\nSELECT COUNT(*)\nFROM %s\n%s;", name, table.FullName(), where)
}

// columnList returns quoted column names joined with ", ".
func columnList(cols []*Column) string {
	var names []string
	for _, c := range cols {
		names = append(names, c.Quoted())
	}
	return strings.Join(names, ", ")
}

// placeholders returns "$1, $2, ..., $n".
func placeholders(n int) string {
	var parts []string
	for i := 1; i <= n; i++ {
		parts = append(parts, fmt.Sprintf("$%d", i))
	}
	return strings.Join(parts, ", ")
}

// generateCreate builds the CREATE query using NonSerialColumns.
func (g *Generator) generateCreate(table *Table) string {
	name := g.queryName("Create", table)
	cols := table.NonSerialColumns()
	colNames := columnList(cols)
	vals := placeholders(len(cols))

	return fmt.Sprintf("-- name: %s :one\nINSERT INTO %s (%s)\nVALUES (%s)\nRETURNING *;",
		name, table.FullName(), colNames, vals)
}

// generateCreateBatch builds the CREATE BATCH query using NonSerialColumns.
func (g *Generator) generateCreateBatch(table *Table) string {
	name := g.queryName("CreateBatch", table)
	cols := table.NonSerialColumns()
	colNames := columnList(cols)
	vals := placeholders(len(cols))

	return fmt.Sprintf("-- name: %s :batchone\nINSERT INTO %s (%s)\nVALUES (%s)\nRETURNING *;",
		name, table.FullName(), colNames, vals)
}

// generateCreateCopy builds the CREATE COPY query using NonSerialColumns.
func (g *Generator) generateCreateCopy(table *Table) string {
	name := g.queryName("CreateCopy", table)
	cols := table.NonSerialColumns()
	colNames := columnList(cols)
	vals := placeholders(len(cols))

	return fmt.Sprintf("-- name: %s :copyfrom\nINSERT INTO %s (%s)\nVALUES (%s);",
		name, table.FullName(), colNames, vals)
}

// generateCreateDefault builds the CREATE DEFAULT query using InsertableColumns.
func (g *Generator) generateCreateDefault(table *Table) string {
	name := g.queryName("CreateDefault", table)
	cols := table.InsertableColumns()
	colNames := columnList(cols)
	vals := placeholders(len(cols))

	return fmt.Sprintf("-- name: %s :one\nINSERT INTO %s (%s)\nVALUES (%s)\nRETURNING *;",
		name, table.FullName(), colNames, vals)
}

// generateCreateCopyDefault builds the CREATE COPY DEFAULT query using InsertableColumns.
func (g *Generator) generateCreateCopyDefault(table *Table) string {
	name := g.queryName("CreateCopyDefault", table)
	cols := table.InsertableColumns()
	colNames := columnList(cols)
	vals := placeholders(len(cols))

	return fmt.Sprintf("-- name: %s :copyfrom\nINSERT INTO %s (%s)\nVALUES (%s);",
		name, table.FullName(), colNames, vals)
}

// generateUpdate builds the UPDATE query.
func (g *Generator) generateUpdate(table *Table) string {
	name := g.queryName("Update", table)
	cols := table.UpdatableColumns()
	if len(cols) == 0 {
		return ""
	}

	var setClauses []string
	for _, col := range cols {
		if col.Nullable {
			setClauses = append(
				setClauses,
				fmt.Sprintf(
					`%s = CASE WHEN sqlc.arg('null_%s')::bool = TRUE THEN NULL ELSE COALESCE(sqlc.narg('%s'), %s) END`,
					col.Quoted(),
					col.Name,
					col.Name,
					col.Quoted(),
				),
			)
		} else {
			setClauses = append(setClauses,
				fmt.Sprintf(`%s = COALESCE(sqlc.narg('%s'), %s)`,
					col.Quoted(), col.Name, col.Quoted()))
		}
	}

	setStr := strings.Join(setClauses, ",\n    ")

	pkName := "id"
	if len(table.PrimaryKeys) > 0 {
		pkName = table.PrimaryKeys[0].Name
	}
	return fmt.Sprintf("-- name: %s :one\nUPDATE %s\nSET %s\nWHERE %q = sqlc.arg('%s')\nRETURNING *;",
		name, table.FullName(), setStr, pkName, pkName)
}

// generateDelete builds the DELETE query.
func (g *Generator) generateDelete(table *Table) string {
	name := g.queryName("Delete", table)
	where := filterWhereClause(table)

	return fmt.Sprintf("-- name: %s :exec\nDELETE FROM %s\n%s;", name, table.FullName(), where)
}

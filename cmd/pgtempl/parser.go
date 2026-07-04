package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	pgv6 "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// Column represents a single column in a database table.
type Column struct {
	Name       string
	Type       string // base pg type: "varchar", "int8", "timestamptz", etc.
	Nullable   bool
	PrimaryKey bool
	Serial     bool // SERIAL/BIGSERIAL/SMALLSERIAL or GENERATED AS IDENTITY
	HasDefault bool
}

// Quoted returns the column name wrapped in double quotes.
func (c *Column) Quoted() string {
	return fmt.Sprintf(`"%s"`, c.Name)
}

// IsRangeFilterable returns true for numeric/date/timestamp types,
// false if the column name contains "id".
func (c *Column) IsRangeFilterable() bool {
	lowerType := strings.ToLower(c.Type)
	lowerName := strings.ToLower(c.Name)

	if strings.Contains(lowerName, "id") {
		return false
	}

	rangeTypes := []string{
		"int", "integer", "bigint", "smallint",
		"decimal", "numeric", "real", "float", "double",
		"timestamp", "timestamptz", "date", "time",
	}
	for _, rt := range rangeTypes {
		if strings.Contains(lowerType, rt) {
			return true
		}
	}
	return false
}

// UniqueConstraint represents a UNIQUE constraint on one or more columns.
type UniqueConstraint struct {
	Name    string
	Columns []string
}

// Table represents a parsed database table.
type Table struct {
	Schema            string
	Name              string
	Columns           []*Column
	PrimaryKeys       []*Column
	UniqueConstraints []*UniqueConstraint
}

// FullName returns "schema"."table".
func (t *Table) FullName() string {
	return fmt.Sprintf(`"%s"."%s"`, t.Schema, t.Name)
}

// QualifiedName returns schema.table.
func (t *Table) QualifiedName() string {
	return t.Schema + "." + t.Name
}

// SafeFileName returns schema_table.
func (t *Table) SafeFileName() string {
	return t.Schema + "_" + t.Name
}

// NonSerialColumns returns all columns except serial ones.
func (t *Table) NonSerialColumns() []*Column {
	var cols []*Column
	for _, c := range t.Columns {
		if !c.Serial {
			cols = append(cols, c)
		}
	}
	return cols
}

// InsertableColumns returns non-serial columns that have no default.
func (t *Table) InsertableColumns() []*Column {
	var cols []*Column
	for _, c := range t.Columns {
		if !c.Serial && !c.HasDefault {
			cols = append(cols, c)
		}
	}
	return cols
}

// UpdatableColumns returns non-serial and non-pk columns.
func (t *Table) UpdatableColumns() []*Column {
	var cols []*Column
	for _, c := range t.Columns {
		if !c.Serial && !c.PrimaryKey {
			cols = append(cols, c)
		}
	}
	return cols
}

// HasVectorColumn reports whether any column is a pgvector type.
func (t *Table) HasVectorColumn() bool {
	for _, c := range t.Columns {
		if c.IsVector() {
			return true
		}
	}
	return false
}

// IsVector reports whether the column is a pgvector type.
func (c *Column) IsVector() bool {
	// Strip optional "(dims)" suffix, then match exact base type name.
	base := strings.ToLower(c.Type)
	if i := strings.IndexByte(base, '('); i != -1 {
		base = base[:i]
	}
	return base == "vector" || base == "sparsevec" || base == "halfvec"
}

// FilterableColumns returns columns usable in WHERE-clause filters.
// Vector columns are excluded — equality/ANY filters on embeddings are meaningless.
func (t *Table) FilterableColumns() []*Column {
	return slices.DeleteFunc(slices.Clone(t.Columns), func(c *Column) bool {
		return c.IsVector()
	})
}

// IdentifierSets returns the primary key first, then each unique constraint
// as sets of columns that can uniquely identify a row.
func (t *Table) IdentifierSets() [][]*Column {
	var sets [][]*Column

	if len(t.PrimaryKeys) > 0 {
		sets = append(sets, t.PrimaryKeys)
	}

	for _, uc := range t.UniqueConstraints {
		var cols []*Column
		for _, colName := range uc.Columns {
			for _, c := range t.Columns {
				if c.Name == colName {
					cols = append(cols, c)
					break
				}
			}
		}
		if len(cols) == len(uc.Columns) {
			sets = append(sets, cols)
		}
	}

	return sets
}

// ParseSchemaFiles reads multiple SQL migration files (in order) and parses
// all CREATE TABLE, ALTER TABLE ADD COLUMN, and CREATE UNIQUE INDEX statements.
// Later files can add columns to tables defined in earlier files.
func ParseSchemaFiles(filenames []string) ([]*Table, error) {
	tableMap := make(map[string]*Table)
	var tableOrder []string

	for _, filename := range filenames {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filename, err)
		}
		sql := string(data)

		serialCols := prescanSerials(sql)

		tree, err := pgquery.Parse(sql)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filename, err)
		}

		for _, rawStmt := range tree.GetStmts() {
			switch n := rawStmt.GetStmt().GetNode().(type) {
			case *pgv6.Node_CreateStmt:
				tbl := parseCreateStmt(n.CreateStmt, serialCols)
				if tbl != nil {
					key := tbl.QualifiedName()
					tableMap[key] = tbl
					tableOrder = append(tableOrder, key)
				}

			case *pgv6.Node_IndexStmt:
				parseIndexStmt(n.IndexStmt, tableMap)

			case *pgv6.Node_AlterTableStmt:
				parseAlterTableStmt(n.AlterTableStmt, tableMap, serialCols)
			}
		}
	}

	tables := make([]*Table, 0, len(tableOrder))
	seen := make(map[string]bool)
	for _, key := range tableOrder {
		if !seen[key] {
			seen[key] = true
			tables = append(tables, tableMap[key])
		}
	}

	return tables, nil
}

// prescanSerials does a full-text scan of the raw SQL to detect
// SERIAL / BIGSERIAL / SMALLSERIAL columns in CREATE TABLE blocks,
// since pg_query transforms these to int4/int8/int2 in the grammar.
// GENERATED AS IDENTITY is handled by the AST (ColumnDef.Identity), not here.
// Returns a set keyed by "schema.table.column".
//
// Migrations write CREATE TABLE headers across multiple lines, e.g.
//
//	CREATE TABLE
//	  IF NOT EXISTS "catalog"."search_sync" (
//
// so we scan the whole string rather than line-by-line. Each match locates a
// table body; SERIAL columns are then found inside that body.
func prescanSerials(sql string) map[string]bool {
	result := make(map[string]bool)

	reTable := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"([^"]+)"\."([^"]+)"\s*\(`)
	reSerial := regexp.MustCompile(`(?im)^\s*"([^"]+)"\s+\w*SERIAL`)
	reBodyEnd := regexp.MustCompile(`(?m)^\s*\)\s*;`)

	for _, m := range reTable.FindAllStringSubmatchIndex(sql, -1) {
		schema := sql[m[2]:m[3]]
		table := sql[m[4]:m[5]]
		bodyStart := m[1]
		rest := sql[bodyStart:]
		endLoc := reBodyEnd.FindStringIndex(rest)
		if endLoc == nil {
			continue
		}
		body := rest[:endLoc[0]]
		for _, sm := range reSerial.FindAllStringSubmatch(body, -1) {
			result[schema+"."+table+"."+sm[1]] = true
		}
	}

	return result
}

// parseCreateStmt extracts a Table from a CREATE TABLE AST node.
func parseCreateStmt(cs *pgv6.CreateStmt, serialCols map[string]bool) *Table {
	if cs.GetRelation() == nil {
		return nil
	}

	tbl := &Table{
		Schema: cs.GetRelation().GetSchemaname(),
		Name:   cs.GetRelation().GetRelname(),
	}

	// First pass: parse columns.
	for _, elt := range cs.GetTableElts() {
		switch n := elt.GetNode().(type) {
		case *pgv6.Node_ColumnDef:
			col := parseColumnDef(n.ColumnDef, tbl.Schema, tbl.Name, serialCols)
			if col != nil {
				tbl.Columns = append(tbl.Columns, col)
				if col.PrimaryKey {
					tbl.PrimaryKeys = append(tbl.PrimaryKeys, col)
				}
			}
		}
	}

	// Second pass: parse table-level constraints (PRIMARY KEY, UNIQUE).
	for _, elt := range cs.GetTableElts() {
		switch n := elt.GetNode().(type) {
		case *pgv6.Node_Constraint:
			parseTableConstraint(n.Constraint, tbl)
		}
	}

	return tbl
}

// parseAlterTableStmt handles ALTER TABLE ADD COLUMN statements,
// merging new columns into existing tables.
func parseAlterTableStmt(stmt *pgv6.AlterTableStmt, tableMap map[string]*Table, serialCols map[string]bool) {
	if stmt.GetRelation() == nil {
		return
	}

	tableKey := stmt.GetRelation().GetSchemaname() + "." + stmt.GetRelation().GetRelname()
	tbl, ok := tableMap[tableKey]
	if !ok {
		return
	}

	for _, cmd := range stmt.GetCmds() {
		atCmd, ok := cmd.GetNode().(*pgv6.Node_AlterTableCmd)
		if !ok {
			continue
		}

		// AT_DropColumn — remove column from columns, PKs, and unique constraints that reference it
		if atCmd.AlterTableCmd.GetSubtype() == pgv6.AlterTableType_AT_DropColumn {
			name := atCmd.AlterTableCmd.GetName()
			tbl.Columns = slices.DeleteFunc(tbl.Columns, func(c *Column) bool {
				return c.Name == name
			})
			tbl.PrimaryKeys = slices.DeleteFunc(tbl.PrimaryKeys, func(c *Column) bool {
				return c.Name == name
			})
			tbl.UniqueConstraints = slices.DeleteFunc(tbl.UniqueConstraints, func(uc *UniqueConstraint) bool {
				return slices.Contains(uc.Columns, name)
			})
			continue
		}

		// AT_AddColumn
		if atCmd.AlterTableCmd.GetSubtype() != pgv6.AlterTableType_AT_AddColumn {
			continue
		}

		defNode := atCmd.AlterTableCmd.GetDef()
		if defNode == nil {
			continue
		}

		colDefNode, ok := defNode.GetNode().(*pgv6.Node_ColumnDef)
		if !ok {
			continue
		}

		col := parseColumnDef(colDefNode.ColumnDef, tbl.Schema, tbl.Name, serialCols)
		if col == nil {
			continue
		}

		// Check for GENERATED AS IDENTITY via the identity field
		if colDefNode.ColumnDef.GetIdentity() != "" {
			col.Serial = true
			col.Nullable = false
			if !col.HasDefault {
				col.HasDefault = true
			}
		}

		tbl.Columns = append(tbl.Columns, col)
		if col.PrimaryKey {
			tbl.PrimaryKeys = append(tbl.PrimaryKeys, col)
		}
	}
}

// parseColumnDef extracts a Column from a ColumnDef AST node.
func parseColumnDef(cd *pgv6.ColumnDef, schema, table string, serialCols map[string]bool) *Column {
	col := &Column{
		Name:     cd.GetColname(),
		Nullable: true, // default to nullable
	}

	// Resolve type name.
	if cd.GetTypeName() != nil {
		col.Type = resolveTypeName(cd.GetTypeName())
	}

	// Check if this was a SERIAL column in the original SQL.
	serialKey := schema + "." + table + "." + col.Name
	if serialCols[serialKey] {
		col.Serial = true
	}

	// Check ColumnDef.IsNotNull field.
	if cd.GetIsNotNull() {
		col.Nullable = false
	}

	// Check for GENERATED AS IDENTITY.
	if cd.GetIdentity() != "" {
		col.Serial = true
		col.Nullable = false
		col.HasDefault = true
	}

	// Walk column-level constraints.
	for _, cNode := range cd.GetConstraints() {
		if cn, ok := cNode.GetNode().(*pgv6.Node_Constraint); ok {
			switch cn.Constraint.GetContype() {
			case pgv6.ConstrType_CONSTR_NOTNULL:
				col.Nullable = false
			case pgv6.ConstrType_CONSTR_DEFAULT:
				col.HasDefault = true
			case pgv6.ConstrType_CONSTR_PRIMARY:
				col.PrimaryKey = true
				col.Nullable = false
			case pgv6.ConstrType_CONSTR_IDENTITY:
				col.Serial = true
				col.Nullable = false
				col.HasDefault = true
			case pgv6.ConstrType_CONSTR_TYPE_UNDEFINED, pgv6.ConstrType_CONSTR_NULL,
				pgv6.ConstrType_CONSTR_GENERATED, pgv6.ConstrType_CONSTR_CHECK,
				pgv6.ConstrType_CONSTR_UNIQUE, pgv6.ConstrType_CONSTR_EXCLUSION,
				pgv6.ConstrType_CONSTR_FOREIGN, pgv6.ConstrType_CONSTR_ATTR_DEFERRABLE,
				pgv6.ConstrType_CONSTR_ATTR_NOT_DEFERRABLE,
				pgv6.ConstrType_CONSTR_ATTR_DEFERRED,
				pgv6.ConstrType_CONSTR_ATTR_IMMEDIATE:
			}
		}
	}

	return col
}

// resolveTypeName converts a TypeName AST node into a string representation.
func resolveTypeName(tn *pgv6.TypeName) string {
	var parts []string
	for _, nameNode := range tn.GetNames() {
		if s, ok := nameNode.GetNode().(*pgv6.Node_String_); ok {
			parts = append(parts, s.String_.GetSval())
		}
	}

	// Strip "pg_catalog." prefix for built-in types.
	if len(parts) >= 2 && parts[0] == "pg_catalog" {
		parts = parts[1:]
	}

	typeName := strings.Join(parts, ".")

	// Append type modifiers like (100) for VARCHAR(100) or (3) for TIMESTAMPTZ(3).
	if len(tn.GetTypmods()) > 0 {
		var mods []string
		for _, modNode := range tn.GetTypmods() {
			switch v := modNode.GetNode().(type) {
			case *pgv6.Node_Integer:
				mods = append(mods, strconv.Itoa(int(v.Integer.GetIval())))
			case *pgv6.Node_AConst:
				if ival, ok := v.AConst.GetVal().(*pgv6.A_Const_Ival); ok {
					mods = append(mods, strconv.Itoa(int(ival.Ival.GetIval())))
				}
			}
		}
		if len(mods) > 0 {
			typeName += "(" + strings.Join(mods, ",") + ")"
		}
	}

	return typeName
}

// parseTableConstraint handles table-level PRIMARY KEY and UNIQUE constraints.
func parseTableConstraint(c *pgv6.Constraint, tbl *Table) {
	switch c.GetContype() {
	case pgv6.ConstrType_CONSTR_PRIMARY:
		for _, keyNode := range c.GetKeys() {
			if s, ok := keyNode.GetNode().(*pgv6.Node_String_); ok {
				colName := s.String_.GetSval()
				for _, col := range tbl.Columns {
					if col.Name == colName {
						col.PrimaryKey = true
						col.Nullable = false
						tbl.PrimaryKeys = append(tbl.PrimaryKeys, col)
						break
					}
				}
			}
		}

	case pgv6.ConstrType_CONSTR_UNIQUE:
		uc := &UniqueConstraint{Name: c.GetConname()}
		for _, keyNode := range c.GetKeys() {
			if s, ok := keyNode.GetNode().(*pgv6.Node_String_); ok {
				uc.Columns = append(uc.Columns, s.String_.GetSval())
			}
		}
		if len(uc.Columns) > 0 {
			tbl.UniqueConstraints = append(tbl.UniqueConstraints, uc)
		}

	case pgv6.ConstrType_CONSTR_TYPE_UNDEFINED, pgv6.ConstrType_CONSTR_NULL,
		pgv6.ConstrType_CONSTR_NOTNULL, pgv6.ConstrType_CONSTR_DEFAULT,
		pgv6.ConstrType_CONSTR_IDENTITY, pgv6.ConstrType_CONSTR_GENERATED,
		pgv6.ConstrType_CONSTR_CHECK, pgv6.ConstrType_CONSTR_EXCLUSION,
		pgv6.ConstrType_CONSTR_FOREIGN, pgv6.ConstrType_CONSTR_ATTR_DEFERRABLE,
		pgv6.ConstrType_CONSTR_ATTR_NOT_DEFERRABLE,
		pgv6.ConstrType_CONSTR_ATTR_DEFERRED,
		pgv6.ConstrType_CONSTR_ATTR_IMMEDIATE:
	}
}

// parseIndexStmt handles CREATE UNIQUE INDEX statements.
func parseIndexStmt(idx *pgv6.IndexStmt, tableMap map[string]*Table) {
	if !idx.GetUnique() {
		return
	}
	if idx.GetRelation() == nil {
		return
	}
	// Partial unique indexes (CREATE UNIQUE INDEX ... WHERE ...) only enforce
	// uniqueness over the subset of rows matching the predicate, so they do
	// NOT uniquely identify a row globally. Skip them as identifier sets.
	if idx.GetWhereClause() != nil {
		return
	}

	tableKey := idx.GetRelation().GetSchemaname() + "." + idx.GetRelation().GetRelname()
	tbl, ok := tableMap[tableKey]
	if !ok {
		return
	}

	uc := &UniqueConstraint{Name: idx.GetIdxname()}
	for _, paramNode := range idx.GetIndexParams() {
		if ie, ok := paramNode.GetNode().(*pgv6.Node_IndexElem); ok {
			uc.Columns = append(uc.Columns, ie.IndexElem.GetName())
		}
	}

	if len(uc.Columns) > 0 {
		tbl.UniqueConstraints = append(tbl.UniqueConstraints, uc)
	}
}

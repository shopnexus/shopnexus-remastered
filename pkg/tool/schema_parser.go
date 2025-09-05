package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Column struct {
	Name         string
	Type         string
	IsNullable   bool
	IsPrimaryKey bool
	IsSerial     bool
	DefaultValue string
}

// GetQuotedName returns the column name wrapped in double quotes
func (c *Column) GetQuotedName() string {
	return fmt.Sprintf("\"%s\"", c.Name)
}

type UniqueConstraint struct {
	Name    string
	Columns []string
}

type Table struct {
	Schema            string
	Name              string
	Columns           []*Column
	PrimaryKey        []*Column
	Constraints       []string
	UniqueConstraints []*UniqueConstraint
}

type SchemaParser struct{}

func NewSchemaParser() *SchemaParser {
	return &SchemaParser{}
}

func (p *SchemaParser) ParseSchema(filename string) ([]*Table, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tables []*Table
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Only parse table definitions
		if p.isCreateTable(line) {
			table, err := p.parseTable(scanner, line)
			if err != nil {
				return nil, err
			}
			if table != nil {
				tables = append(tables, table)
			}
		}
	}

	// Second pass: parse unique indexes
	if err := p.parseUniqueIndexes(filename, tables); err != nil {
		return nil, err
	}

	return tables, scanner.Err()
}

func (p *SchemaParser) isCreateTable(line string) bool {
	return strings.HasPrefix(line, "CREATE TABLE")
}

func (p *SchemaParser) parseTable(scanner *bufio.Scanner, firstLine string) (*Table, error) {
	// Extract schema and table name from CREATE TABLE "schema"."table" (
	re := regexp.MustCompile(`CREATE TABLE "([^"]+)"\.?"([^"]+)" \(`)
	matches := re.FindStringSubmatch(firstLine)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax: %s", firstLine)
	}

	table := &Table{
		Schema:  matches[1],
		Name:    matches[2],
		Columns: []*Column{},
	}

	// Parse columns
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// End of table definition
		if strings.HasPrefix(line, ");") {
			break
		}

		// Parse constraints
		if strings.HasPrefix(line, "CONSTRAINT") {
			table.Constraints = append(table.Constraints, line)
			p.parseConstraint(line, table)
			continue
		}

		// Parse column definition
		column := p.parseColumn(line)
		if column != nil {
			table.Columns = append(table.Columns, column)
			if column.IsPrimaryKey {
				table.PrimaryKey = append(table.PrimaryKey, column)
			}
		}
	}

	return table, nil
}

func (p *SchemaParser) parseColumn(line string) *Column {
	// Remove trailing comma
	line = strings.TrimSuffix(line, ",")

	// Skip if it's not a column definition
	if !strings.HasPrefix(line, "\"") {
		return nil
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil
	}

	column := &Column{
		Name: strings.Trim(parts[0], "\""),
		Type: parts[1],
	}

	// Parse column attributes
	fullLine := strings.Join(parts[2:], " ")

	// Check for NOT NULL - be more specific to avoid false positives
	column.IsNullable = !strings.Contains(strings.ToUpper(fullLine), "NOT NULL")

	// Check for PRIMARY KEY
	column.IsPrimaryKey = strings.Contains(fullLine, "PRIMARY KEY")

	// Check for SERIAL types
	column.IsSerial = strings.Contains(column.Type, "SERIAL")

	// Check for DEFAULT values
	if defaultIdx := strings.Index(strings.ToUpper(fullLine), "DEFAULT"); defaultIdx != -1 {
		// Find the actual DEFAULT keyword in the original case
		defaultStart := strings.Index(fullLine, "DEFAULT")
		if defaultStart == -1 {
			defaultStart = strings.Index(fullLine, "default")
		}

		if defaultStart != -1 {
			defaultPart := strings.TrimSpace(fullLine[defaultStart+7:])

			// Handle various endings after the default value (comma, space, end of line)
			endChars := []string{",", " ", "\t", "\n"}
			endIdx := len(defaultPart)

			for _, endChar := range endChars {
				if idx := strings.Index(defaultPart, endChar); idx != -1 && idx < endIdx {
					endIdx = idx
				}
			}

			if endIdx > 0 {
				column.DefaultValue = strings.TrimSpace(defaultPart[:endIdx])
			} else {
				column.DefaultValue = strings.TrimSpace(defaultPart)
			}

			// If we found any default value, even an empty one, mark it as having a default
			if defaultStart != -1 {
				// Set a marker that this column has a default, even if we couldn't parse the exact value
				if column.DefaultValue == "" {
					column.DefaultValue = "DEFAULT" // placeholder to indicate it has a default
				}
			}
		}
	}

	return column
}

func (p *SchemaParser) parseConstraint(line string, table *Table) {
	// Parse PRIMARY KEY constraint: CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id")
	if strings.Contains(line, "PRIMARY KEY") {
		re := regexp.MustCompile(`PRIMARY KEY \(([^)]+)\)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			columnsStr := matches[1]
			colParts := strings.Split(columnsStr, ",")
			for _, colPart := range colParts {
				colName := strings.Trim(strings.TrimSpace(colPart), "\"")
				// Find the column in the table and mark it as primary key
				for _, col := range table.Columns {
					if col.Name == colName {
						col.IsPrimaryKey = true
						table.PrimaryKey = append(table.PrimaryKey, col)
						break
					}
				}
			}
		}
	}
}

func (p *SchemaParser) parseUniqueIndexes(filename string, tables []*Table) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a map for quick table lookup
	tableMap := make(map[string]*Table)
	for _, table := range tables {
		key := fmt.Sprintf("%s.%s", table.Schema, table.Name)
		tableMap[key] = table
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "CREATE UNIQUE INDEX") {
			p.parseUniqueIndex(line, tableMap)
		}
	}

	return scanner.Err()
}

func (p *SchemaParser) parseUniqueIndex(line string, tableMap map[string]*Table) {
	// Parse: CREATE UNIQUE INDEX "index_name" ON "schema"."table"("col1", "col2");
	re := regexp.MustCompile(`CREATE UNIQUE INDEX "([^"]+)" ON "([^"]+)"\.?"([^"]+)"\(([^)]+)\);?`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 5 {
		return
	}

	indexName := matches[1]
	schema := matches[2]
	tableName := matches[3]
	columnsStr := matches[4]

	// Find the table
	tableKey := fmt.Sprintf("%s.%s", schema, tableName)
	table, exists := tableMap[tableKey]
	if !exists {
		return
	}

	// Parse column names
	var columns []string
	colParts := strings.Split(columnsStr, ",")
	for _, col := range colParts {
		// Remove quotes and trim whitespace
		colName := strings.Trim(strings.TrimSpace(col), "\"")
		columns = append(columns, colName)
	}

	// Add unique constraint to table
	constraint := &UniqueConstraint{
		Name:    indexName,
		Columns: columns,
	}
	table.UniqueConstraints = append(table.UniqueConstraints, constraint)
}

// Helper methods for template generation
func (t *Table) GetNonSerialColumns() []*Column {
	var columns []*Column
	for _, col := range t.Columns {
		if !col.IsSerial {
			columns = append(columns, col)
		}
	}
	return columns
}

func (t *Table) GetUpdatableColumns() []*Column {
	var columns []*Column
	for _, col := range t.Columns {
		if !col.IsSerial && !col.IsPrimaryKey {
			columns = append(columns, col)
		}
	}
	return columns
}

func (t *Table) GetFilterableColumns() []*Column {
	var columns []*Column
	for _, col := range t.Columns {
		// Include most columns except large text fields for filtering
		if !strings.Contains(strings.ToLower(col.Type), "text") || col.Name == "code" {
			columns = append(columns, col)
		}
	}
	return columns
}

func (t *Table) GetPrimaryKeyColumns() []*Column {
	return t.PrimaryKey
}

func (t *Table) HasDateColumns() bool {
	for _, col := range t.Columns {
		if strings.Contains(col.Name, "date_") {
			return true
		}
	}
	return false
}

func (t *Table) GetFullTableName() string {
	return fmt.Sprintf("\"%s\".\"%s\"", t.Schema, t.Name)
}

func (t *Table) GetSchemaName() string {
	return t.Schema
}

func (t *Table) GetQualifiedName() string {
	return t.Schema + "." + t.Name
}

func (t *Table) GetSafeFileName() string {
	return t.Schema + "_" + t.Name
}

// GetNonSerialNonDefaultColumns returns columns that are not serial and don't have default values
func (t *Table) GetNonSerialNonDefaultColumns() []*Column {
	var columns []*Column
	for _, col := range t.Columns {
		if !col.IsSerial && col.DefaultValue == "" {
			columns = append(columns, col)
		}
	}
	return columns
}

// GetAllIdentifierConstraints returns all possible ways to identify a record (primary key + unique constraints)
func (t *Table) GetAllIdentifierConstraints() [][]*Column {
	var constraints [][]*Column

	// Add primary key as first constraint
	if len(t.PrimaryKey) > 0 {
		constraints = append(constraints, t.PrimaryKey)
	}

	// Add unique constraints
	for _, uniqueConstraint := range t.UniqueConstraints {
		var constraintCols []*Column
		for _, colName := range uniqueConstraint.Columns {
			// Find the column in the table
			for _, col := range t.Columns {
				if col.Name == colName {
					constraintCols = append(constraintCols, col)
					break
				}
			}
		}
		if len(constraintCols) == len(uniqueConstraint.Columns) {
			constraints = append(constraints, constraintCols)
		}
	}

	return constraints
}

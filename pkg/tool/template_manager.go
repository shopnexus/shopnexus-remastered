package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

type TemplateManager struct {
	templateDir string
	templates   map[string]*template.Template
}

func NewTemplateManager(templateDir string) *TemplateManager {
	return &TemplateManager{
		templateDir: templateDir,
		templates:   make(map[string]*template.Template),
	}
}

func (tm *TemplateManager) LoadTemplates() error {
	// Create template directory if it doesn't exist
	if err := os.MkdirAll(tm.templateDir, 0755); err != nil {
		return err
	}

	// Load all template files
	templateFiles := []string{"get", "list", "create", "update", "delete"}

	for _, templateName := range templateFiles {
		templateFile := filepath.Join(tm.templateDir, templateName+".sql.tmpl")
		if _, err := os.Stat(templateFile); os.IsNotExist(err) {
			continue // Skip if template doesn't exist
		}

		// Use the filename as template name for ParseFiles to work correctly
		tmpl, err := template.New(templateName + ".sql.tmpl").Funcs(tm.getTemplateFuncs()).ParseFiles(templateFile)
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", templateName, err)
		}

		tm.templates[templateName] = tmpl
	}

	return nil
}

func (tm *TemplateManager) GenerateQuery(queryType string, table *Table) (string, error) {
	tmpl, exists := tm.templates[queryType]
	if !exists {
		return "", nil // Return empty if template doesn't exist
	}

	var buf bytes.Buffer
	// Execute the specific template by name (the filename)
	if err := tmpl.ExecuteTemplate(&buf, queryType+".sql.tmpl", table); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (tm *TemplateManager) getTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"join":   strings.Join,
		"title":  strings.Title,
		"lower":  strings.ToLower,
		"upper":  strings.ToUpper,
		"printf": fmt.Sprintf,
		"camelCase": func(s string) string {
			parts := strings.Split(s, "_")
			for i := range parts {
				if i == 0 {
					parts[i] = strings.ToLower(parts[i])
				} else {
					parts[i] = strings.Title(parts[i])
				}
			}
			return strings.Join(parts, "")
		},
		"pascalCase": func(s string) string {
			parts := strings.Split(s, "_")
			for i := range parts {
				parts[i] = strings.Title(parts[i])
			}
			return strings.Join(parts, "")
		},
		"sqlcArg": func(name string) string {
			return fmt.Sprintf("sqlc.arg('%s')", name)
		},
		"sqlcNarg": func(name string) string {
			return fmt.Sprintf("sqlc.narg('%s')", name)
		},
		"increment": func(i int) int {
			return i + 1
		},
		"quotedName": func(col *Column) string {
			return col.GetQuotedName()
		},
		"joinQuotedColumns": func(columns []*Column, separator string) string {
			var quoted []string
			for _, col := range columns {
				quoted = append(quoted, col.GetQuotedName())
			}
			return strings.Join(quoted, separator)
		},
		"generateWhereConditions": func(table *Table) string {
			constraints := table.GetAllIdentifierConstraints()
			if len(constraints) == 0 {
				return `WHERE "id" = $1`
			}

			var conditions []string
			paramIndex := 1

			for _, constraint := range constraints {
				var constraintParts []string
				for _, col := range constraint {
					//constraintParts = append(constraintParts, fmt.Sprintf("%s = $%d", col.GetQuotedName(), paramIndex))
					constraintParts = append(constraintParts, fmt.Sprintf("%s = sqlc.narg('%s')", col.GetQuotedName(), col.Name))
					paramIndex++
				}
				conditions = append(conditions, fmt.Sprintf("(%s)", strings.Join(constraintParts, " AND ")))
			}

			return "WHERE " + strings.Join(conditions, " OR ")
		},
		"getType": func(col *Column) string {
			return getPostgreSQLArrayType(col.Type)
		},
		"isRangeFilterable": func(col *Column) bool {
			return isRangeFilterableColumn(col)
		},
		"generateFilterConditions": func(table *Table) string {
			return generateFilterConditions(table)
		},
	}
}

// Helper function to map column data types to PostgreSQL array types
func getPostgreSQLArrayType(dataType string) string {
	switch strings.ToLower(dataType) {
	case "int", "integer", "int4":
		return "integer"
	case "bigint", "int8", "bigserial":
		return "bigint"
	case "smallint", "int2":
		return "smallint"
	case "varchar", "text", "string":
		return "text"
	case "uuid":
		return "uuid"
	case "boolean", "bool":
		return "boolean"
	case "decimal", "numeric":
		return "numeric"
	case "timestamp", "timestamptz":
		return "timestamp"
	case "date":
		return "date"
	default:
		return "text" // fallback to text for unknown types
	}
}

// Helper function to check if a column can be filtered by range (from/to)
func isRangeFilterableColumn(col *Column) bool {
	// Check if column type supports range queries (numeric and date types)
	lowerType := strings.ToLower(col.Type)
	rangeTypes := []string{
		"int", "integer", "int4", "bigint", "int8", "bigserial", "serial", "serial8",
		"smallint", "int2", "smallserial", "serial2", "serial4",
		"decimal", "numeric", "real", "float4", "double", "float8",
		"timestamp", "timestamptz", "date", "time", "timetz",
	}
	
	for _, rangeType := range rangeTypes {
		if strings.Contains(lowerType, rangeType) {
			return true
		}
	}
	return false
}

// Helper function to generate filter conditions combining exact match (ANY) and range (from/to)
func generateFilterConditions(table *Table) string {
	var conditions []string
	
	for _, col := range table.GetFilterableColumns() {
		if isRangeFilterableColumn(col) {
			// For range filterable columns, add both exact match and range conditions
			conditions = append(conditions, fmt.Sprintf("(%s = ANY(sqlc.slice('%s')) OR sqlc.slice('%s') IS NULL)", col.GetQuotedName(), col.Name, col.Name))
			conditions = append(conditions, fmt.Sprintf("(%s >= sqlc.narg('%s_from') OR sqlc.narg('%s_from') IS NULL)", col.GetQuotedName(), col.Name, col.Name))
			conditions = append(conditions, fmt.Sprintf("(%s <= sqlc.narg('%s_to') OR sqlc.narg('%s_to') IS NULL)", col.GetQuotedName(), col.Name, col.Name))
		} else {
			// For non-range columns, only exact match
			conditions = append(conditions, fmt.Sprintf("(%s = ANY(sqlc.slice('%s')) OR sqlc.slice('%s') IS NULL)", col.GetQuotedName(), col.Name, col.Name))
		}
	}
	
	if len(conditions) > 0 {
		return "WHERE (\n    " + strings.Join(conditions, " AND\n    ") + "\n)"
	}
	return ""
}

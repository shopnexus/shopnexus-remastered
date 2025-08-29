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
					constraintParts = append(constraintParts, fmt.Sprintf("%s = $%d", col.GetQuotedName(), paramIndex))
					paramIndex++
				}
				conditions = append(conditions, fmt.Sprintf("(%s)", strings.Join(constraintParts, " AND ")))
			}
			
			return "WHERE " + strings.Join(conditions, " OR ")
		},
	}
}

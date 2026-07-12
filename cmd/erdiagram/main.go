// erdiagram runs `tbls out` to get the database schema, then embeds per-module
// Mermaid ER diagrams into each module's README.md between
// <!--START_SECTION:mermaid--> and <!--END_SECTION:mermaid--> markers.
//
// Usage:
//
//	go run ./cmd/erdiagram/
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"shopnexus-server/config"
	"sort"
	"strings"
)

type Schema struct {
	Tables    []Table    `json:"tables"`
	Relations []Relation `json:"relations"`
}

type Table struct {
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Constraint struct {
	Type    string   `json:"type"` // "p" = PK, "f" = FK, "u" = unique, "n" = not null
	Columns []string `json:"columns"`
}

type Relation struct {
	Table             string   `json:"table"`
	Columns           []string `json:"columns"`
	Cardinality       string   `json:"cardinality"`
	ParentTable       string   `json:"parent_table"`
	ParentCardinality string   `json:"parent_cardinality"`
}

const (
	modulePath = "internal/module"
	startTag   = "<!--START_SECTION:mermaid-->"
	endTag     = "<!--END_SECTION:mermaid-->"
)

var typeReplacements = map[string]string{
	"timestamp with time zone":       "timestamptz",
	"timestamp(3) with time zone":    "timestamptz",
	"timestamp without time zone":    "timestamp",
	"timestamp(3) without time zone": "timestamp",
	"double precision":               "float8",
}

var typePrefixStrips = []string{
	`"order".`, "account.", "catalog.", "chat.",
	"inventory.", "promotion.", "analytic.", "common.", "system.",
}

var excludePrefixes = []string{
	"public.",
}

func main() {
	schema := loadSchema()

	entries, err := os.ReadDir(modulePath)
	if err != nil {
		log.Fatalf("read module dir: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		readmePath := filepath.Join(modulePath, module, "README.md")

		if _, err := os.Stat(readmePath); os.IsNotExist(err) {
			continue
		}

		mermaid := buildModuleDiagram(module, schema)
		if mermaid == "" {
			continue
		}

		if err := embedInReadme(readmePath, mermaid); err != nil {
			log.Printf("skip %s: %v", module, err)
			continue
		}
		fmt.Printf("  %s: updated\n", module)
	}
}

func loadSchema() Schema {
	cfg, err := config.New()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	dsn := cfg.Postgres.Url

	out, err := exec.Command("tbls", "out", "-t", "json", dsn).Output()
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			log.Fatalf("tbls out failed: %s\n%s", err, exitErr.Stderr)
		}
		log.Fatalf("tbls out: %v", err)
	}

	var schema Schema
	if err := json.Unmarshal(out, &schema); err != nil {
		log.Fatalf("parse tbls output: %v", err)
	}

	// Filter out excluded tables
	var filtered []Table
	for _, t := range schema.Tables {
		exclude := false
		for _, prefix := range excludePrefixes {
			if strings.HasPrefix(t.Name, prefix) {
				exclude = true
				break
			}
		}
		if !exclude {
			filtered = append(filtered, t)
		}
	}
	schema.Tables = filtered

	return schema
}

func buildModuleDiagram(module string, schema Schema) string {
	schemaPrefix := module + "."

	// Build lookup structures
	tableMap := map[string]Table{}
	fkColumns := map[string]map[string]bool{}
	moduleTableSet := map[string]bool{}
	var moduleTables []string

	for _, t := range schema.Tables {
		tableMap[t.Name] = t
		fks := map[string]bool{}
		for _, c := range t.Constraints {
			if c.Type == "f" {
				for _, col := range c.Columns {
					fks[col] = true
				}
			}
		}
		fkColumns[t.Name] = fks

		if strings.HasPrefix(t.Name, schemaPrefix) {
			moduleTables = append(moduleTables, t.Name)
			moduleTableSet[t.Name] = true
		}
	}

	if len(moduleTables) == 0 {
		return ""
	}
	sort.Strings(moduleTables)

	var b strings.Builder
	b.WriteString("```mermaid\nerDiagram\n")

	// Relations where at least one side belongs to this module
	for _, rel := range schema.Relations {
		if !moduleTableSet[rel.Table] && !moduleTableSet[rel.ParentTable] {
			continue
		}

		childCard := cardinalitySymbol(rel.Cardinality)
		parentCard := cardinalitySymbol(rel.ParentCardinality)
		label := ""
		if len(rel.Columns) > 0 {
			label = rel.Columns[0]
		}

		fmt.Fprintf(&b, "\"%s\" %s--%s \"%s\" : \"%s\"\n",
			rel.Table, childCard, parentCard, rel.ParentTable, label)
	}
	b.WriteString("\n")

	// Entity definitions (only tables in this module)
	for _, tableName := range moduleTables {
		t := tableMap[tableName]
		fks := fkColumns[tableName]

		fmt.Fprintf(&b, "\"%s\" {\n", tableName)
		for _, col := range t.Columns {
			typ := cleanTypeName(col.Type)
			fkMarker := ""
			if fks[col.Name] {
				fkMarker = " FK"
			}
			fmt.Fprintf(&b, "  %s %s%s\n", typ, col.Name, fkMarker)
		}
		b.WriteString("}\n")
	}

	b.WriteString("```")
	return b.String()
}

func cardinalitySymbol(c string) string {
	switch c {
	case "exactly_one":
		return "||"
	case "zero_or_one":
		return "|o"
	case "zero_or_more":
		return "}o"
	case "one_or_more":
		return "}|"
	default:
		return "}o"
	}
}

func cleanTypeName(typ string) string {
	if r, ok := typeReplacements[typ]; ok {
		return r
	}
	for _, prefix := range typePrefixStrips {
		if after, ok := strings.CutPrefix(typ, prefix); ok {
			return after
		}
	}
	return typ
}

func embedInReadme(path, mermaid string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	text := string(content)
	startIdx := strings.Index(text, startTag)
	endIdx := strings.Index(text, endTag)

	if startIdx == -1 || endIdx == -1 {
		return fmt.Errorf("missing %s / %s markers", startTag, endTag)
	}

	before := text[:startIdx+len(startTag)]
	after := text[endIdx:]
	newContent := before + "\n" + mermaid + "\n" + after

	return os.WriteFile(path, []byte(newContent), 0644)
}

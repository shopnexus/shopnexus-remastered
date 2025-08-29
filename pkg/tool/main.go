package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var (
		schemaFile  = flag.String("schema", "", "Path to SQL migration file")
		outputDir   = flag.String("output", "queries", "Output directory for generated SQL files")
		tableName   = flag.String("table", "", "Specific table name to generate queries for (format: schema.table, optional)")
		templateDir = flag.String("templates", "pkg/tool/templates", "Directory containing template files")
		singleFile  = flag.Bool("single-file", false, "Generate all queries into a single file (only when table not specified)")
		help        = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help || *schemaFile == "" {
		showHelp()
		return
	}

	generator := NewQueryGenerator(*templateDir)
	if err := generator.GenerateFromSchema(*schemaFile, *outputDir, *tableName, *singleFile); err != nil {
		log.Fatalf("Error generating queries: %v", err)
	}

	fmt.Printf("Successfully generated SQLC queries in %s\n", *outputDir)
}

func showHelp() {
	fmt.Println("SQLC Query Generator")
	fmt.Println("Generate SQLC queries from SQL schema files")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run tool/main.go -schema <schema_file> [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -schema <file>     Path to SQL migration file (required)")
	fmt.Println("  -output <dir>      Output directory for generated SQL files (default: queries)")
	fmt.Println("  -table <name>      Generate queries for specific table (format: schema.table or just table)")
	fmt.Println("  -templates <dir>   Directory containing template files (default: tool/templates)")
	fmt.Println("  -single-file       Generate all queries into a single file (only when table not specified)")
	fmt.Println("  -help              Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Generate queries for all tables")
	fmt.Println("  go run tool/main.go -schema prisma/migrations/0_init/migration.sql")
	fmt.Println()
	fmt.Println("  # Generate queries for specific table")
	fmt.Println("  go run tool/main.go -schema prisma/migrations/0_init/migration.sql -table account.account")
	fmt.Println()
	fmt.Println("  # Custom output directory")
	fmt.Println("  go run tool/main.go -schema prisma/migrations/0_init/migration.sql -output generated_queries")
	fmt.Println()
	fmt.Println("  # Generate all queries into a single file")
	fmt.Println("  go run tool/main.go -schema prisma/migrations/0_init/migration.sql -single-file")
}

type QueryGenerator struct {
	templateDir string
	templates   *TemplateManager
}

func NewQueryGenerator(templateDir string) *QueryGenerator {
	return &QueryGenerator{
		templateDir: templateDir,
		templates:   NewTemplateManager(templateDir),
	}
}

func (g *QueryGenerator) GenerateFromSchema(schemaFile, outputDir, specificTable string, singleFile bool) error {
	// Parse the schema file
	parser := NewSchemaParser()
	tables, err := parser.ParseSchema(schemaFile)
	if err != nil {
		return fmt.Errorf("failed to parse schema: %w", err)
	}

	// Load templates
	if err := g.templates.LoadTemplates(); err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Filter tables if specific table is requested
	var tablesToGenerate []*Table
	for _, table := range tables {
		if specificTable != "" {
			tableFullName := table.Schema + "." + table.Name
			if tableFullName != specificTable && table.Name != specificTable {
				continue
			}
		}
		tablesToGenerate = append(tablesToGenerate, table)
	}

	// Generate queries
	if singleFile && specificTable == "" {
		// Generate all queries into a single file
		return g.generateAllTablesQueries(tablesToGenerate, outputDir)
	} else {
		// Generate separate files for each table
		for _, table := range tablesToGenerate {
			outputFile := filepath.Join(outputDir, table.Schema+"_"+table.Name+".sql")
			if err := g.generateTableQueries(table, outputFile); err != nil {
				return fmt.Errorf("failed to generate queries for table %s.%s: %w", table.Schema, table.Name, err)
			}
			fmt.Printf("Generated queries for table: %s.%s\n", table.Schema, table.Name)
		}
	}

	return nil
}

func (g *QueryGenerator) generateTableQueries(table *Table, outputFile string) error {
	var queries []string

	// Generate different types of queries
	queryTypes := []string{"get", "list", "create", "update", "delete"}

	for _, queryType := range queryTypes {
		query, err := g.templates.GenerateQuery(queryType, table)
		if err != nil {
			return fmt.Errorf("failed to generate %s query: %w", queryType, err)
		}
		if query != "" {
			queries = append(queries, query)
		}
	}

	// Write to file
	content := strings.Join(queries, "\n\n")
	return os.WriteFile(outputFile, []byte(content), 0644)
}

func (g *QueryGenerator) generateAllTablesQueries(tables []*Table, outputDir string) error {
	var allQueries []string

	// Add header comment
	header := []string{
		"-- Code generated by tool/main.go. DO NOT EDIT.",
		"-- This file contains all queries for the database schema.",
		"",
	}
	allQueries = append(allQueries, strings.Join(header, "\n"))

	for _, table := range tables {
		var tableQueries []string

		// Add a comment header for the table
		tableQueries = append(tableQueries, fmt.Sprintf("-- ========================================"))
		tableQueries = append(tableQueries, fmt.Sprintf("-- Queries for table: %s.%s", table.Schema, table.Name))
		tableQueries = append(tableQueries, fmt.Sprintf("-- ========================================"))

		// Generate different types of queries
		queryTypes := []string{"get", "list", "create", "update", "delete"}

		for _, queryType := range queryTypes {
			query, err := g.templates.GenerateQuery(queryType, table)
			if err != nil {
				return fmt.Errorf("failed to generate %s query for table %s.%s: %w", queryType, table.Schema, table.Name, err)
			}
			if query != "" {
				tableQueries = append(tableQueries, query)
			}
		}

		allQueries = append(allQueries, strings.Join(tableQueries, "\n\n"))
		fmt.Printf("Generated queries for table: %s.%s\n", table.Schema, table.Name)
	}

	// Write all queries to a single file
	outputFile := filepath.Join(outputDir, "queries.sql")
	content := strings.Join(allQueries, "\n\n")
	if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write queries file: %w", err)
	}

	fmt.Printf("All queries written to: %s\n", outputFile)
	return nil
}

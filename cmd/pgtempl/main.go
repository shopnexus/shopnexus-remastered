package main

import (
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	var (
		module           = flag.String("module", "", "Module name (e.g. account, catalog)")
		outputDir        = flag.String("output", "", "Output directory (default: internal/module/<module>/db/queries)")
		tableName        = flag.String("table", "", "Specific table (format: schema.table)")
		singleFile       = flag.String("single-file", "", "Generate all queries into a single file")
		skipSchemaPrefix = flag.Bool("skip-schema-prefix", false, "Query names without schema prefix")
		emit             = flag.String("emit", "sql", "What to emit: sql (sqlc query templates) | repo (Go list/pagination repo) | crud (Go pgx CRUD repo)")
		help             = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help || *module == "" {
		fmt.Println("pgtempl - SQLC Query Generator")
		fmt.Println()
		fmt.Println("Reads all migration files for a module and generates SQLC queries.")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  go run ./cmd/pgtempl/ -module <name> [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  -module <name>       Module name, e.g. account, catalog, or 'all' (required)")
		fmt.Println("  -output <dir>        Output directory (default: internal/module/<module>/db/queries)")
		fmt.Println("  -table <name>        Generate for specific table (schema.table)")
		fmt.Println("  -single-file         All queries in one file")
		fmt.Println("  -skip-schema-prefix  Query names without schema prefix")
		fmt.Println("  -help                Show this help")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  go run ./cmd/pgtempl/ -module all")
		fmt.Println("  go run ./cmd/pgtempl/ -module account")
		fmt.Println("  go run ./cmd/pgtempl/ -module catalog -table catalog.product_spu")
		return
	}

	// Discover modules to process
	modules := []string{*module}
	if *module == "all" {
		var err error
		modules, err = discoverModules()
		if err != nil {
			log.Fatalf("Error discovering modules: %v", err)
		}
		if len(modules) == 0 {
			log.Fatal("No modules with migrations found")
		}
		fmt.Printf("Found %d modules: %s\n", len(modules), strings.Join(modules, ", "))
	}

	for _, mod := range modules {
		if err := generateForModule(mod, *outputDir, *tableName, *singleFile, *skipSchemaPrefix, *emit); err != nil {
			log.Printf("Error generating for module %s: %v", mod, err)
		}
	}
}

// generateForModule generates queries (emit=sql) or Go repo code (emit=repo)
// for a single module.
func generateForModule(module, outputDir, tableName string, singleFile string, skipSchemaPrefix bool, emit string) error {
	migrationsDir := filepath.Join("internal", "module", module, "db", "migrations")
	files, err := discoverMigrations(migrationsDir)
	if err != nil {
		return fmt.Errorf("discover migrations: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no migration files in %s", migrationsDir)
	}

	tables, err := ParseSchemaFiles(files)
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}

	if tableName != "" {
		var filtered []*Table
		for _, t := range tables {
			if t.QualifiedName() == tableName || t.Name == tableName {
				filtered = append(filtered, t)
			}
		}
		tables = filtered
	}

	if emit == "crud" {
		modelDir := filepath.Join("internal", "module", module, "model")
		models, pkgName, err := ParseModelDirWithPkg(modelDir)
		if err != nil {
			return fmt.Errorf("parse models: %w", err)
		}
		out := outputDir
		if out == "" {
			out = filepath.Join("internal", "module", module, "repo")
		}
		if err := os.MkdirAll(out, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
		gen := &CrudGenerator{
			Package:   module + "repo",
			Receiver:  "Repository",
			ModelPkg:  pkgName,
			ModelPath: "shopnexus-server/internal/module/" + module + "/model",
		}
		writeCrudFile(tables, models, gen, out)
		fmt.Printf("[%s] Generated crud+list repo in %s\n", module, out)
		return nil
	}

	if emit == "repo" {
		out := outputDir
		if out == "" {
			out = filepath.Join("internal", "module", module, "db", "sqlc")
		}
		if err := os.MkdirAll(out, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
		gen := &RepoGenerator{Package: module + "db", IncludeSchema: !skipSchemaPrefix}
		writeRepoFiles(tables, gen, out)
		fmt.Printf("[%s] Generated repo list code in %s\n", module, out)
		return nil
	}

	out := outputDir
	if out == "" {
		out = filepath.Join("internal", "module", module, "db", "queries")
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	gen := &Generator{IncludeSchema: !skipSchemaPrefix}

	if singleFile != "" && tableName == "" {
		writeSingleFile(tables, gen, out, singleFile)
	} else {
		writePerTable(tables, gen, out)
	}

	fmt.Printf("[%s] Generated SQLC queries in %s\n", module, out)
	return nil
}

// discoverModules finds all modules that have a db/migrations directory.
func discoverModules() ([]string, error) {
	modulesDir := filepath.Join("internal", "module")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", modulesDir, err)
	}

	var modules []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		migrationsDir := filepath.Join(modulesDir, e.Name(), "db", "migrations")
		if info, err := os.Stat(migrationsDir); err == nil && info.IsDir() {
			modules = append(modules, e.Name())
		}
	}
	sort.Strings(modules)
	return modules, nil
}

// discoverMigrations finds all *.up.sql files in the given directory, sorted by name.
func discoverMigrations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func writePerTable(tables []*Table, gen *Generator, outputDir string) {
	for _, t := range tables {
		header := fmt.Sprintf(
			"-- Code generated by pgtempl. DO NOT EDIT.\n-- Queries for table: %s.%s\n\n",
			t.Schema,
			t.Name,
		)
		content := header + gen.Generate(t)
		path := filepath.Join(outputDir, t.SafeFileName()+".sql")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			log.Fatalf("Error writing %s: %v", path, err)
		}
		fmt.Printf("Generated queries for table: %s.%s\n", t.Schema, t.Name)
	}
}

// writeRepoFiles emits one <schema>_<table>_gen_list.go per eligible table.
func writeRepoFiles(tables []*Table, gen *RepoGenerator, outputDir string) {
	for _, t := range tables {
		body := gen.GenerateRepo(t)
		if body == "" {
			fmt.Printf("Skipping repo (key-only/vector): %s.%s\n", t.Schema, t.Name)
			continue
		}
		formatted, err := format.Source([]byte(body))
		if err != nil {
			log.Fatalf("gofmt %s.%s: %v\nraw:\n%s", t.Schema, t.Name, err, body)
		}
		path := filepath.Join(outputDir, t.SafeFileName()+"_gen_list.go")
		if err := os.WriteFile(path, formatted, 0644); err != nil {
			log.Fatalf("write %s: %v", path, err)
		}
		fmt.Printf("Generated repo list for table: %s.%s\n", t.Schema, t.Name)
	}
}

// writeCrudFile emits all tables' CRUD+List into one repo_gen.go. Each table is
// matched to its entity struct via the //pgtempl:table marker; tables without
// a marker match are skipped.
func writeCrudFile(tables []*Table, models map[string]*ModelStruct, gen *CrudGenerator, outputDir string) {
	byTable := map[string]*ModelStruct{}
	for _, m := range models {
		if m.Table != "" {
			byTable[m.Table] = m
		}
	}
	var items []crudItem
	for _, t := range tables {
		m, ok := byTable[t.FullName()]
		if !ok {
			fmt.Printf("Skipping crud (no //pgtempl:table marker): %s.%s\n", t.Schema, t.Name)
			continue
		}
		items = append(items, crudItem{t, m})
	}

	body, err := gen.GenerateFile(items)
	if err != nil {
		log.Fatalf("crud: %v", err)
	}
	if body == "" {
		fmt.Println("No crud generated (no matching entities)")
		return
	}
	formatted, err := format.Source([]byte(body))
	if err != nil {
		log.Fatalf("gofmt crud: %v\nraw:\n%s", err, body)
	}
	path := filepath.Join(outputDir, "repo_gen.go")
	if err := os.WriteFile(path, formatted, 0644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	fmt.Printf("Generated crud (%d tables) in %s\n", len(items), path)
}

func writeSingleFile(tables []*Table, gen *Generator, outputDir string, singleFile string) {
	var sections []string
	sections = append(
		sections,
		"-- Code generated by pgtempl. DO NOT EDIT.\n-- This file contains all queries for the database schema.\n",
	)

	for _, t := range tables {
		content := gen.Generate(t)
		if content == "" {
			fmt.Printf("Skipping table (key-only): %s.%s\n", t.Schema, t.Name)
			continue
		}
		header := fmt.Sprintf(
			"-- ========================================\n-- Queries for table: %s.%s\n-- ========================================",
			t.Schema,
			t.Name,
		)
		sections = append(sections, header+"\n\n"+content)
		fmt.Printf("Generated queries for table: %s.%s\n", t.Schema, t.Name)
	}

	path := filepath.Join(outputDir, singleFile)
	result := strings.Join(sections, "\n\n")
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		log.Fatalf("Error writing %s: %v", path, err)
	}
	fmt.Printf("All queries written to: %s\n", path)
}

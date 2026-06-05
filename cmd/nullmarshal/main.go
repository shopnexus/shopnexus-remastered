package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	pattern := "internal/module/*/db/sqlc/models.go"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}
	if len(matches) == 0 {
		fmt.Println("no models.go files found")
		return
	}
	for _, path := range matches {
		if err := processFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "error processing %s: %v\n", path, err)
		} else {
			fmt.Println("processed", path)
		}
	}
}

type nullTypeInfo struct {
	typeName  string
	innerName string
}

func processFile(path string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	var nullTypes []nullTypeInfo
	hasMethod := map[string]bool{}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if !strings.HasPrefix(ts.Name.Name, "Null") {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				if len(st.Fields.List) != 2 {
					continue
				}
				lastField := st.Fields.List[1]
				if isIdent(lastField.Type, "bool") && lastField.Names != nil && lastField.Names[0].Name == "Valid" {
					nullTypes = append(nullTypes, nullTypeInfo{
						typeName:  ts.Name.Name,
						innerName: st.Fields.List[0].Names[0].Name,
					})
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				continue
			}
			typeName := receiverTypeName(d.Recv.List[0].Type)
			if typeName == "" {
				continue
			}
			if strings.HasPrefix(typeName, "Null") && (d.Name.Name == "MarshalJSON" || d.Name.Name == "UnmarshalJSON") {
				hasMethod[typeName] = true
			}
		}
	}

	var toAdd []nullTypeInfo
	for _, nt := range nullTypes {
		if !hasMethod[nt.typeName] {
			toAdd = append(toAdd, nt)
		}
	}

	if len(toAdd) == 0 {
		return nil
	}

	fmt.Println("  adding MarshalJSON to", len(toAdd), "types in", path)

	// Generate methods as text, format, then append
	var buf bytes.Buffer
	for _, nt := range toAdd {
		buf.WriteString(fmt.Sprintf(`func (n %[1]s) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.%[2]s)
}
func (n *%[1]s) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.Valid = false
		return nil
	}
	n.Valid = true
	return json.Unmarshal(b, &n.%[2]s)
}
`, nt.typeName, nt.innerName))
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format methods: %w", err)
	}

	// Read original, append formatted methods, format whole
	orig, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	full := append(orig, '\n')
	full = append(full, src...)

	// Ensure encoding/json is imported (some modules don't have it)
	full = ensureJSONImport(full)

	formatted, err := format.Source(full)
	if err != nil {
		return fmt.Errorf("format full: %w", err)
	}

	return os.WriteFile(path, formatted, 0644)
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

var importJSON = regexp.MustCompile(`"encoding/json"`)

func ensureJSONImport(src []byte) []byte {
	if importJSON.Match(src) {
		return src
	}
	// Add encoding/json to the import block
	re := regexp.MustCompile(`(\t"time"\n)`)
	if re.Match(src) {
		return re.ReplaceAll(src, []byte("$1\t\"encoding/json\"\n"))
	}
	re = regexp.MustCompile(`(\t"github\.com/google/uuid"\n)`)
	if re.Match(src) {
		return re.ReplaceAll(src, []byte("$1\t\"encoding/json\"\n"))
	}
	return src
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

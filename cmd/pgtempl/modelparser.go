package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"reflect"
	"strings"
)

// ModelField is one db-tagged struct field.
type ModelField struct {
	GoName string // "ParentID"
	GoType string // rendered type expr, e.g. "uuid.NullUUID", "orderdb.OrderRefundStatus"
	DBTag  string // "parent_id"
}

// ModelStruct is a parsed entity: its db-tagged fields and the imports their
// types reference (localName -> import path).
type ModelStruct struct {
	Name    string
	Table   string // from //pgtempl:table "schema"."table" marker; empty if absent
	Fields  []ModelField
	ByDB    map[string]ModelField
	Imports map[string]string
}

// ParseModelDir parses all .go files in dir and returns the structs it finds,
// keyed by struct name. Generated *_gen.go files declare no db-tagged structs,
// so re-parsing the output dir is harmless.
func ParseModelDir(dir string) (map[string]*ModelStruct, error) {
	m, _, _, err := ParseModelDirWithPkg(dir)
	return m, err
}

// ParseModelDirWithPkg is like ParseModelDir but also returns the package name
// and the set of all type names declared in the package (structs, enums, etc.).
func ParseModelDirWithPkg(dir string) (map[string]*ModelStruct, string, map[string]bool, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		return nil, "", nil, err
	}
	out := make(map[string]*ModelStruct)
	localTypes := make(map[string]bool)
	pkgName := ""
	for name, pkg := range pkgs {
		pkgName = name
		for _, file := range pkg.Files {
			for n, ms := range collectStructs(fset, file) {
				out[n] = ms
			}
			// Collect all type names declared in this file.
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					localTypes[ts.Name.Name] = true
				}
			}
		}
	}
	return out, pkgName, localTypes, nil
}

// fileImports maps each import's local name to its path. A named import uses the
// name; an unnamed one uses the last path segment.
func fileImports(file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		local := path
		if i := strings.LastIndexByte(path, '/'); i != -1 {
			local = path[i+1:]
		}
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = path
	}
	return imports
}

// tableMarker extracts the //pgtempl:table "schema"."table" value from a
// TypeSpec or GenDecl doc comment, returning "" if absent.
func tableMarker(specDoc, declDoc *ast.CommentGroup) string {
	for _, cg := range []*ast.CommentGroup{specDoc, declDoc} {
		if cg == nil {
			continue
		}
		for _, c := range cg.List {
			line := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if v, ok := strings.CutPrefix(line, "pgtempl:table "); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func collectStructs(fset *token.FileSet, file *ast.File) map[string]*ModelStruct {
	imports := fileImports(file)
	out := make(map[string]*ModelStruct)

	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			ms := &ModelStruct{
				Name:    ts.Name.Name,
				Table:   tableMarker(ts.Doc, gd.Doc),
				ByDB:    map[string]ModelField{},
				Imports: map[string]string{},
			}
			for _, field := range st.Fields.List {
				if field.Tag == nil || len(field.Names) != 1 {
					continue // skip embedded / multi-name / untagged
				}
				tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
				db := tag.Get("db")
				if db == "" || db == "-" {
					continue
				}
				mf := ModelField{
					GoName: field.Names[0].Name,
					GoType: exprString(fset, field.Type),
					DBTag:  db,
				}
				ms.Fields = append(ms.Fields, mf)
				ms.ByDB[db] = mf
				for local := range usedQualifiers(field.Type) {
					if path, ok := imports[local]; ok {
						ms.Imports[local] = path
					}
				}
			}
			out[ms.Name] = ms
		}
	}
	return out
}

// exprString renders a type expression back to Go source ("uuid.NullUUID").
func exprString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, e)
	return b.String()
}

// usedQualifiers returns the package local-names referenced by a type expr
// (the X of every selector, e.g. "uuid" in uuid.NullUUID).
func usedQualifiers(e ast.Expr) map[string]bool {
	found := map[string]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				found[id.Name] = true
			}
		}
		return true
	})
	return found
}

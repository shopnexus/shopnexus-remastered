// genrestate generates a Restate HTTP proxy struct that implements a given interface
// by delegating each method to the Restate ingress endpoint via restateclient.Call/Send.
//
// Minimal usage (from the same package as the interface):
//
//	//go:generate go run shopnexus-server/cmd/genrestate -interface OrderClient -service OrderBiz
//
// This will read interface.go, detect the package name, and write restate_gen.go.
//
// All flags:
//
//	-src         source file (default: interface.go)
//	-interface   interface name to implement (required)
//	-service     Restate service name (required)
//	-type        restate client struct name (default: <service>RestateClient, e.g. OrderBizProxy)
//	-out         output file (default: restate_gen.go)
//	-pkg         output package name (default: read from source file)
//	-srcpkg      source package alias (default: same as pkg)
//	-srcpkgpath  source package import path (only needed for cross-package generation)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"strings"
	"unicode"
)

type methodInfo struct {
	Name       string
	InputName  string
	InputType  string
	HasInput   bool
	OutputType string
	HasOutput  bool
}

func main() {
	srcFile := flag.String("src", "interface.go", "source file containing the interface")
	ifaceName := flag.String("interface", "", "interface name to implement (required)")
	serviceName := flag.String("service", "", "Restate service name (required)")
	proxyType := flag.String("type", "", "restate client struct name (default: <service>RestateClient)")
	pkgName := flag.String("pkg", "", "output package name (default: from source file)")
	srcPkg := flag.String("srcpkg", "", "source package alias (default: same as pkg)")
	srcPkgPath := flag.String("srcpkgpath", "", "source package import path (cross-package only)")
	outFile := flag.String("out", "restate_gen.go", "output file path")
	flag.Parse()

	if *ifaceName == "" || *serviceName == "" {
		log.Fatal("required flags: -interface and -service")
	}

	// Parse source
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, *srcFile, nil, parser.AllErrors)
	if err != nil {
		log.Fatalf("parse %s: %v", *srcFile, err)
	}

	// Infer defaults
	if *pkgName == "" {
		*pkgName = f.Name.Name
	}
	if *srcPkg == "" {
		*srcPkg = f.Name.Name
	}
	if *proxyType == "" {
		*proxyType = *serviceName + "RestateClient"
	}

	samePackage := *pkgName == *srcPkg

	// Build import alias → path map from the source file
	importMap := make(map[string]string)
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var alias string
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
		}
		importMap[alias] = path
	}

	// Index every interface declared in the file so we can resolve embedded
	// (composed) interfaces — OrderBiz embeds CartBiz, RefundBiz, … and each
	// sub-interface's methods must be expanded into the generated proxy.
	ifaceMap := make(map[string]*ast.InterfaceType)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				ifaceMap[ts.Name.Name] = it
			}
		}
	}
	iface, ok := ifaceMap[*ifaceName]
	if !ok {
		log.Fatalf("interface %s not found in %s", *ifaceName, *srcFile)
	}

	// Extract methods, expanding embedded interfaces depth-first. Declaration
	// order is preserved (an embed expands inline at its position); duplicate
	// method names (diamond embeds) are emitted once.
	usedImports := make(map[string]string)
	var methods []methodInfo
	seenMethod := make(map[string]bool)
	visitedIface := make(map[string]bool)

	var collect func(it *ast.InterfaceType)
	collect = func(it *ast.InterfaceType) {
		for _, field := range it.Methods.List {
			// Embedded interface: a field with no name. Resolve it from the
			// same-file index and recurse.
			if len(field.Names) == 0 {
				ident, ok := field.Type.(*ast.Ident)
				if !ok {
					log.Fatalf("embedded interface must be a same-package identifier, got %T", field.Type)
				}
				if visitedIface[ident.Name] {
					continue
				}
				visitedIface[ident.Name] = true
				embedded, ok := ifaceMap[ident.Name]
				if !ok {
					log.Fatalf("embedded interface %s not found in %s", ident.Name, *srcFile)
				}
				collect(embedded)
				continue
			}

			ft, ok := field.Type.(*ast.FuncType)
			if !ok {
				continue
			}
			name := field.Names[0].Name
			if seenMethod[name] {
				continue
			}
			seenMethod[name] = true

			m := methodInfo{Name: name}

			// Second param (skip ctx which is first)
			if ft.Params.NumFields() > 1 {
				param := ft.Params.List[1]
				m.HasInput = true
				if len(param.Names) > 0 {
					m.InputName = param.Names[0].Name
				} else {
					m.InputName = "input"
				}
				m.InputType = renderType(fset, param.Type, *srcPkg, samePackage, usedImports, importMap)
			}

			// Return: (T, error) → has output; (error) → no output
			if ft.Results != nil && ft.Results.NumFields() > 1 {
				m.HasOutput = true
				m.OutputType = renderType(fset, ft.Results.List[0].Type, *srcPkg, samePackage, usedImports, importMap)
			}

			methods = append(methods, m)
		}
	}
	collect(iface)

	// Always needed imports
	usedImports["context"] = "context"
	if !samePackage {
		if *srcPkgPath == "" {
			log.Fatal("-srcpkgpath is required for cross-package generation")
		}
		usedImports[*srcPkg] = *srcPkgPath
	}
	usedImports["restateclient"] = "shopnexus-server/internal/infras/restate"

	// Generate output
	var buf bytes.Buffer
	buf.WriteString("// Code generated by genrestate; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "package %s\n\n", *pkgName)

	// Imports
	buf.WriteString("import (\n")
	for alias, path := range usedImports {
		lastSeg := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			lastSeg = path[i+1:]
		}
		if alias != lastSeg {
			fmt.Fprintf(&buf, "\t%s %q\n", alias, path)
		} else {
			fmt.Fprintf(&buf, "\t%q\n", path)
		}
	}
	buf.WriteString(")\n\n")

	// Const + struct
	ifaceRef := *ifaceName
	if !samePackage {
		ifaceRef = *srcPkg + "." + *ifaceName
	}
	fmt.Fprintf(&buf, "const serviceName = %q\n\n", *serviceName)
	fmt.Fprintf(&buf, "// %s implements %s via Restate HTTP ingress.\n", *proxyType, ifaceRef)
	fmt.Fprintf(&buf, "type %s struct {\n\tclient *restateclient.Client\n}\n\n", *proxyType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", ifaceRef, *proxyType)
	fmt.Fprintf(&buf, "func New%s(restateIngressURL string) *%s {\n", *proxyType, *proxyType)
	fmt.Fprintf(&buf, "\treturn &%s{client: restateclient.NewClient(restateIngressURL)}\n}\n\n", *proxyType)

	// Methods
	for _, m := range methods {
		writeMethod(&buf, *proxyType, m)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalf("gofmt: %v\nraw output:\n%s", err, buf.String())
	}

	if err := os.WriteFile(*outFile, formatted, 0644); err != nil {
		log.Fatalf("write %s: %v", *outFile, err)
	}

	fmt.Printf("genrestate: wrote %s (%d methods)\n", *outFile, len(methods))
}

// renderType converts an AST type expression to a string, qualifying
// unqualified exported identifiers with srcPkg (unless samePackage) and tracking used imports.
func renderType(
	fset *token.FileSet,
	expr ast.Expr,
	srcPkg string,
	samePackage bool,
	usedImports, importMap map[string]string,
) string {
	switch t := expr.(type) {
	case *ast.Ident:
		if !samePackage && len(t.Name) > 0 && unicode.IsUpper(rune(t.Name[0])) {
			return srcPkg + "." + t.Name
		}
		return t.Name

	case *ast.SelectorExpr:
		pkgIdent, ok := t.X.(*ast.Ident)
		if ok {
			if path, exists := importMap[pkgIdent.Name]; exists {
				usedImports[pkgIdent.Name] = path
			}
			return pkgIdent.Name + "." + t.Sel.Name
		}
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, expr)
		return buf.String()

	case *ast.ArrayType:
		return "[]" + renderType(fset, t.Elt, srcPkg, samePackage, usedImports, importMap)

	case *ast.StarExpr:
		return "*" + renderType(fset, t.X, srcPkg, samePackage, usedImports, importMap)

	case *ast.MapType:
		k := renderType(fset, t.Key, srcPkg, samePackage, usedImports, importMap)
		v := renderType(fset, t.Value, srcPkg, samePackage, usedImports, importMap)
		return "map[" + k + "]" + v

	case *ast.IndexExpr:
		x := renderType(fset, t.X, srcPkg, samePackage, usedImports, importMap)
		idx := renderType(fset, t.Index, srcPkg, samePackage, usedImports, importMap)
		return x + "[" + idx + "]"

	case *ast.IndexListExpr:
		x := renderType(fset, t.X, srcPkg, samePackage, usedImports, importMap)
		params := make([]string, len(t.Indices))
		for i, idx := range t.Indices {
			params[i] = renderType(fset, idx, srcPkg, samePackage, usedImports, importMap)
		}
		return x + "[" + strings.Join(params, ", ") + "]"

	default:
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, expr)
		return buf.String()
	}
}

func writeMethod(buf *bytes.Buffer, proxyType string, m methodInfo) {
	buf.WriteString("func (p *" + proxyType + ") " + m.Name + "(ctx context.Context")
	if m.HasInput {
		fmt.Fprintf(buf, ", %s %s", m.InputName, m.InputType)
	}
	buf.WriteString(") ")

	if m.HasOutput {
		fmt.Fprintf(buf, "(%s, error)", m.OutputType)
	} else {
		buf.WriteString("error")
	}
	buf.WriteString(" {\n")

	inputArg := "nil"
	if m.HasInput {
		inputArg = m.InputName
	}

	if m.HasOutput {
		fmt.Fprintf(buf, "\treturn restateclient.Call[%s](ctx, p.client, serviceName, %q, %s)\n",
			m.OutputType, m.Name, inputArg)
	} else {
		fmt.Fprintf(buf, "\treturn restateclient.Send(ctx, p.client, serviceName, %q, %s)\n",
			m.Name, inputArg)
	}

	buf.WriteString("}\n\n")
}

// genrestate generates Restate HTTP proxies for a given interface: a
// request-response client (direct methods, awaited) plus a one-way sender
// facade reachable via Send(), and a response-future facade reachable via
// Future() for parallel or racing calls inside a Restate handler context.
// It also emits <Iface>Sender, <Iface>Future, and <Iface>Client interfaces;
// <Iface>Client is the type consuming modules should inject.
//
// Minimal usage (from the same package as the interface):
//
//	//go:generate go run shopnexus-server/cmd/genrestate -interface OrderClient -service OrderBiz
//
// This will parse the whole package directory of -src (so embedded interfaces
// may live in any file of the package), detect the package name, and write
// restate_gen.go.
//
// All flags:
//
//	-src         entry file; its directory is parsed as the package (default: interface.go)
//	-interface   interface name to implement (required)
//	-service     Restate service name (required)
//	-kind        service kind: service | workflow (default: service)
//	-type        restate client struct name (default: <service>RestateClient, e.g. OrderBizProxy)
//	-out         output file (default: restate_gen.go)
//	-pkg         output package name (default: read from source file)
//	-srcpkg      source package alias (default: same as pkg)
//	-srcpkgpath  source package import path (only needed for cross-package generation)
//
// With -kind workflow every interface method must take the workflow key as
// its second parameter (string or uuid.UUID), e.g.
//
//	Run(ctx context.Context, workflowID uuid.UUID, input RunInput) (RunOutput, error)
//
// and the generated proxies dispatch via restate.Workflow / WorkflowSend
// keyed by that parameter.
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
	"path/filepath"
	"strings"
	"unicode"
)

type methodInfo struct {
	Name       string
	KeyName    string // workflow kind only: the workflow-key parameter
	KeyType    string // "string" or "uuid.UUID"
	InputName  string
	InputType  string
	HasInput   bool
	OutputType string
	HasOutput  bool
}

// keyExpr renders the workflow-key argument as a string expression.
func (m methodInfo) keyExpr() string {
	if m.KeyType == "string" {
		return m.KeyName
	}
	return m.KeyName + ".String()"
}

// params renders the method parameter list starting with the given ctx
// declaration, followed by the workflow key (when present) and the input.
func (m methodInfo) params(ctxDecl string) string {
	s := ctxDecl
	if m.KeyName != "" {
		s += fmt.Sprintf(", %s %s", m.KeyName, m.KeyType)
	}
	if m.HasInput {
		s += fmt.Sprintf(", %s %s", m.InputName, m.InputType)
	}
	return s
}

// ifaceDecl is one interface declaration plus the context needed to render
// its method signatures: the declaring file's import alias → path map, and —
// when the interface lives in another package than -src — the alias + import
// path used to qualify that package's own exported types.
type ifaceDecl struct {
	typ      *ast.InterfaceType
	imports  map[string]string
	pkgAlias string // "" when declared in the source package
	pkgPath  string
}

// findModuleRoot walks up from dir to the enclosing go.mod and returns the
// module name and root directory. Both empty when no go.mod is found.
func findModuleRoot(dir string) (string, string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ""
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(abs, "go.mod"))
		if readErr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if name, ok := strings.CutPrefix(line, "module "); ok {
					return strings.TrimSpace(name), abs
				}
			}
			return "", ""
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", ""
		}
		abs = parent
	}
}

func main() {
	srcFile := flag.String("src", "interface.go", "source file containing the interface")
	ifaceName := flag.String("interface", "", "interface name to implement (required)")
	serviceName := flag.String("service", "", "Restate service name (required)")
	kind := flag.String("kind", "service", "service kind: service | workflow")
	proxyType := flag.String("type", "", "restate client struct name (default: <service>RestateClient)")
	pkgName := flag.String("pkg", "", "output package name (default: from source file)")
	srcPkg := flag.String("srcpkg", "", "source package alias (default: same as pkg)")
	srcPkgPath := flag.String("srcpkgpath", "", "source package import path (cross-package only)")
	outFile := flag.String("out", "restate_gen.go", "output file path")
	flag.Parse()

	if *ifaceName == "" || *serviceName == "" {
		log.Fatal("required flags: -interface and -service")
	}
	if *kind != "service" && *kind != "workflow" {
		log.Fatalf("-kind must be service or workflow, got %q", *kind)
	}
	isWorkflow := *kind == "workflow"

	// Parse the whole package directory: the root interface names embedded
	// sub-interfaces (CartBiz, PaymentBiz, …) that live in per-feature files,
	// not necessarily in -src itself.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, *srcFile, nil, parser.AllErrors)
	if err != nil {
		log.Fatalf("parse %s: %v", *srcFile, err)
	}

	srcDir := filepath.Dir(*srcFile)
	pkgs, err := parser.ParseDir(fset, srcDir, func(fi os.FileInfo) bool {
		// Skip tests and the generated output itself (may be stale or broken).
		return !strings.HasSuffix(fi.Name(), "_test.go") && fi.Name() != filepath.Base(*outFile)
	}, parser.AllErrors)
	if err != nil {
		log.Fatalf("parse dir %s: %v", srcDir, err)
	}
	pkg, ok := pkgs[f.Name.Name]
	if !ok {
		log.Fatalf("package %s not found in %s", f.Name.Name, srcDir)
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

	senderType := strings.TrimSuffix(*proxyType, "Client") + "Sender"
	senderIface := *ifaceName + "Sender"
	futureType := strings.TrimSuffix(*proxyType, "Client") + "Future"
	futureIface := *ifaceName + "Future"
	clientIface := *ifaceName + "Client"

	// Index every interface in a package, each paired with its declaring
	// file's import alias → path map (selector types like sharedmodel.Option
	// must resolve against the aliases of the file that declares the method)
	// and, for foreign packages, the alias + import path used to qualify the
	// package's own exported types in the generated output.
	indexPackage := func(pkg *ast.Package, pkgAlias, pkgPath string, into map[string]ifaceDecl) {
		for _, file := range pkg.Files {
			importMap := make(map[string]string)
			for _, imp := range file.Imports {
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
			for _, decl := range file.Decls {
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
						into[ts.Name.Name] = ifaceDecl{
							typ:      it,
							imports:  importMap,
							pkgAlias: pkgAlias,
							pkgPath:  pkgPath,
						}
					}
				}
			}
		}
	}

	ifaceMap := make(map[string]ifaceDecl)
	indexPackage(pkg, "", "", ifaceMap)

	// pkgIfaceMaps lazily parses same-module packages referenced by embedded
	// interfaces (e.g. `buyerorder.BuyerOrderBiz`), keyed by import path.
	moduleName, moduleRoot := findModuleRoot(srcDir)
	pkgIfaceMaps := map[string]map[string]ifaceDecl{"": ifaceMap}
	loadPackage := func(alias, path string) map[string]ifaceDecl {
		if m, cached := pkgIfaceMaps[path]; cached {
			return m
		}
		if moduleName == "" || !strings.HasPrefix(path, moduleName+"/") {
			log.Fatalf("embedded interface package %q is outside module %q", path, moduleName)
		}
		dir := filepath.Join(moduleRoot, strings.TrimPrefix(path, moduleName+"/"))
		fpkgs, parseErr := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.AllErrors)
		if parseErr != nil {
			log.Fatalf("parse embedded package %s: %v", dir, parseErr)
		}
		m := make(map[string]ifaceDecl)
		for _, fpkg := range fpkgs {
			indexPackage(fpkg, alias, path, m)
		}
		pkgIfaceMaps[path] = m
		return m
	}

	iface, ok := ifaceMap[*ifaceName]
	if !ok {
		log.Fatalf("interface %s not found in package %s (%s)", *ifaceName, f.Name.Name, srcDir)
	}

	// Extract methods, expanding embedded interfaces depth-first. Declaration
	// order is preserved (an embed expands inline at its position); duplicate
	// method names (diamond embeds) are emitted once.
	usedImports := make(map[string]string)
	var methods []methodInfo
	seenMethod := make(map[string]bool)
	visitedIface := make(map[string]bool)

	var collect func(d ifaceDecl)
	collect = func(d ifaceDecl) {
		for _, field := range d.typ.Methods.List {
			// Embedded interface: a field with no name. Same-package idents
			// resolve from the declaring package's index; qualified idents
			// (pkg.Iface) lazily parse that package. Recurse either way.
			if len(field.Names) == 0 {
				switch t := field.Type.(type) {
				case *ast.Ident:
					if visitedIface[d.pkgPath+"."+t.Name] {
						continue
					}
					visitedIface[d.pkgPath+"."+t.Name] = true
					embedded, found := pkgIfaceMaps[d.pkgPath][t.Name]
					if !found {
						log.Fatalf("embedded interface %s not found in package %q", t.Name, d.pkgPath)
					}
					collect(embedded)
				case *ast.SelectorExpr:
					pkgIdent, isIdent := t.X.(*ast.Ident)
					if !isIdent {
						log.Fatalf("embedded interface qualifier must be a package identifier, got %T", t.X)
					}
					path, imported := d.imports[pkgIdent.Name]
					if !imported {
						log.Fatalf("embedded interface package %s not imported by declaring file", pkgIdent.Name)
					}
					if visitedIface[path+"."+t.Sel.Name] {
						continue
					}
					visitedIface[path+"."+t.Sel.Name] = true
					embedded, found := loadPackage(pkgIdent.Name, path)[t.Sel.Name]
					if !found {
						log.Fatalf("embedded interface %s not found in package %q", t.Sel.Name, path)
					}
					collect(embedded)
				default:
					log.Fatalf("unsupported embedded interface expression %T", field.Type)
				}
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

			// Workflow kind: second param is the workflow key, input shifts to third.
			inputIdx := 1
			if isWorkflow {
				if ft.Params.NumFields() < 2 {
					log.Fatalf("workflow method %s must take the workflow key as second parameter", name)
				}
				keyParam := ft.Params.List[1]
				if len(keyParam.Names) > 0 {
					m.KeyName = keyParam.Names[0].Name
				} else {
					m.KeyName = "workflowID"
				}
				m.KeyType = renderType(fset, keyParam.Type, *srcPkg, samePackage, usedImports, d)
				if m.KeyType != "string" && m.KeyType != "uuid.UUID" {
					log.Fatalf("workflow method %s: key must be string or uuid.UUID, got %s", name, m.KeyType)
				}
				inputIdx = 2
			}

			// Input param (skip ctx, and the workflow key when present)
			if ft.Params.NumFields() > inputIdx {
				param := ft.Params.List[inputIdx]
				m.HasInput = true
				if len(param.Names) > 0 {
					m.InputName = param.Names[0].Name
				} else {
					m.InputName = "input"
				}
				m.InputType = renderType(fset, param.Type, *srcPkg, samePackage, usedImports, d)
			}

			// Return: (T, error) → has output; (error) → no output
			if ft.Results != nil && ft.Results.NumFields() > 1 {
				m.HasOutput = true
				m.OutputType = renderType(fset, ft.Results.List[0].Type, *srcPkg, samePackage, usedImports, d)
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
	usedImports["restatec"] = "shopnexus-server/internal/shared/restate"
	usedImports["restate"] = "github.com/restatedev/sdk-go"

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

	ifaceRef := *ifaceName
	if !samePackage {
		ifaceRef = *srcPkg + "." + *ifaceName
	}
	fmt.Fprintf(&buf, "const serviceName = %q\n\n", *serviceName)

	// Sender interface: every method as a one-way call, outputs dropped.
	fmt.Fprintf(
		&buf,
		"// %s mirrors %s as one-way (fire-and-forget) calls; outputs are dropped.\n",
		senderIface,
		ifaceRef,
	)
	fmt.Fprintf(&buf, "type %s interface {\n", senderIface)
	for _, m := range methods {
		fmt.Fprintf(&buf, "\t%s(%s) error\n", m.Name, m.params("ctx context.Context"))
	}
	buf.WriteString("}\n\n")

	// Future interface: every method returns a ResponseFuture for racing/parallel calls.
	fmt.Fprintf(
		&buf,
		"// %s mirrors %s returning response futures for racing\n// or parallel calls. Only usable inside a Restate handler context.\n",
		futureIface,
		ifaceRef,
	)
	fmt.Fprintf(&buf, "type %s interface {\n", futureIface)
	for _, m := range methods {
		outType := "restate.Void"
		if m.HasOutput {
			outType = m.OutputType
		}
		fmt.Fprintf(
			&buf,
			"\t%s(%s) restate.ResponseFuture[%s]\n",
			m.Name,
			m.params("rctx restate.Context"),
			outType,
		)
	}
	buf.WriteString("}\n\n")

	// Client interface: the cross-module DI type.
	fmt.Fprintf(
		&buf,
		"// %s is the cross-module client: direct methods are request-response, Send() is one-way, Future() returns response futures.\n",
		clientIface,
	)
	fmt.Fprintf(
		&buf,
		"type %s interface {\n\t%s\n\tSend() %s\n\tFuture() %s\n}\n\n",
		clientIface,
		ifaceRef,
		senderIface,
		futureIface,
	)

	// Request-response proxy.
	fmt.Fprintf(&buf, "// %s implements %s via Restate HTTP ingress.\n", *proxyType, clientIface)
	fmt.Fprintf(
		&buf,
		"type %s struct {\n\tcall *restatec.CallClient\n\tsend *%s\n\tfuture *%s\n}\n\n",
		*proxyType,
		senderType,
		futureType,
	)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", clientIface, *proxyType)
	fmt.Fprintf(&buf, "func New%s(restateIngressURL string) *%s {\n", *proxyType, *proxyType)
	fmt.Fprintf(
		&buf,
		"\treturn &%s{\n\t\tcall: restatec.NewCallClient(restateIngressURL),\n\t\tsend: &%s{client: restatec.NewSendClient(restateIngressURL)},\n\t\tfuture: &%s{},\n\t}\n}\n\n",
		*proxyType,
		senderType,
		futureType,
	)
	fmt.Fprintf(&buf, "func (p *%s) Send() %s { return p.send }\n\n", *proxyType, senderIface)
	fmt.Fprintf(&buf, "func (p *%s) Future() %s { return p.future }\n\n", *proxyType, futureIface)

	// Request-response methods.
	for _, m := range methods {
		writeCallMethod(&buf, *proxyType, m, isWorkflow)
	}

	// One-way proxy.
	fmt.Fprintf(&buf, "// %s implements %s.\n", senderType, senderIface)
	fmt.Fprintf(&buf, "type %s struct {\n\tclient *restatec.SendClient\n}\n\n", senderType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", senderIface, senderType)
	for _, m := range methods {
		writeSendMethod(&buf, senderType, m, isWorkflow)
	}

	// Future proxy.
	fmt.Fprintf(&buf, "// %s implements %s via the Restate SDK.\n", futureType, futureIface)
	fmt.Fprintf(&buf, "type %s struct{}\n\n", futureType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", futureIface, futureType)
	for _, m := range methods {
		writeFutureMethod(&buf, futureType, m, isWorkflow)
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
// unqualified exported identifiers with their declaring package (the alias of
// a foreign embedded-interface package, or srcPkg for cross-package output)
// and tracking used imports.
func renderType(
	fset *token.FileSet,
	expr ast.Expr,
	srcPkg string,
	samePackage bool,
	usedImports map[string]string,
	d ifaceDecl,
) string {
	switch t := expr.(type) {
	case *ast.Ident:
		if len(t.Name) > 0 && unicode.IsUpper(rune(t.Name[0])) {
			// Exported ident declared in a foreign embedded-interface package:
			// qualify with that package's alias regardless of output package.
			if d.pkgAlias != "" {
				usedImports[d.pkgAlias] = d.pkgPath
				return d.pkgAlias + "." + t.Name
			}
			if !samePackage {
				return srcPkg + "." + t.Name
			}
		}
		return t.Name

	case *ast.SelectorExpr:
		pkgIdent, ok := t.X.(*ast.Ident)
		if ok {
			if path, exists := d.imports[pkgIdent.Name]; exists {
				usedImports[pkgIdent.Name] = path
			}
			return pkgIdent.Name + "." + t.Sel.Name
		}
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, expr)
		return buf.String()

	case *ast.ArrayType:
		return "[]" + renderType(fset, t.Elt, srcPkg, samePackage, usedImports, d)

	case *ast.StarExpr:
		return "*" + renderType(fset, t.X, srcPkg, samePackage, usedImports, d)

	case *ast.MapType:
		k := renderType(fset, t.Key, srcPkg, samePackage, usedImports, d)
		v := renderType(fset, t.Value, srcPkg, samePackage, usedImports, d)
		return "map[" + k + "]" + v

	case *ast.IndexExpr:
		x := renderType(fset, t.X, srcPkg, samePackage, usedImports, d)
		idx := renderType(fset, t.Index, srcPkg, samePackage, usedImports, d)
		return x + "[" + idx + "]"

	case *ast.IndexListExpr:
		x := renderType(fset, t.X, srcPkg, samePackage, usedImports, d)
		params := make([]string, len(t.Indices))
		for i, idx := range t.Indices {
			params[i] = renderType(fset, idx, srcPkg, samePackage, usedImports, d)
		}
		return x + "[" + strings.Join(params, ", ") + "]"

	default:
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, expr)
		return buf.String()
	}
}

// writeCallMethod emits a request-response method on the call proxy.
// Methods with an output use Call[T]; error-only methods use CallVoid —
// both await completion. Workflow methods route through the CallWorkflow
// variants keyed by the workflow-key parameter.
func writeCallMethod(buf *bytes.Buffer, proxyType string, m methodInfo, isWorkflow bool) {
	fmt.Fprintf(buf, "func (p *%s) %s(%s) ", proxyType, m.Name, m.params("ctx context.Context"))

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

	switch {
	case isWorkflow && m.HasOutput:
		fmt.Fprintf(buf, "\treturn restatec.CallWorkflow[%s](ctx, p.call, serviceName, %s, %q, %s)\n",
			m.OutputType, m.keyExpr(), m.Name, inputArg)
	case isWorkflow:
		fmt.Fprintf(buf, "\treturn restatec.CallWorkflowVoid(ctx, p.call, serviceName, %s, %q, %s)\n",
			m.keyExpr(), m.Name, inputArg)
	case m.HasOutput:
		fmt.Fprintf(buf, "\treturn restatec.Call[%s](ctx, p.call, serviceName, %q, %s)\n",
			m.OutputType, m.Name, inputArg)
	default:
		fmt.Fprintf(buf, "\treturn restatec.CallVoid(ctx, p.call, serviceName, %q, %s)\n",
			m.Name, inputArg)
	}

	buf.WriteString("}\n\n")
}

// writeSendMethod emits a one-way method on the sender proxy; outputs are dropped.
func writeSendMethod(buf *bytes.Buffer, senderType string, m methodInfo, isWorkflow bool) {
	fmt.Fprintf(buf, "func (s *%s) %s(%s) error {\n", senderType, m.Name, m.params("ctx context.Context"))

	inputArg := "nil"
	if m.HasInput {
		inputArg = m.InputName
	}
	if isWorkflow {
		fmt.Fprintf(buf, "\treturn restatec.SendWorkflow(ctx, s.client, serviceName, %s, %q, %s)\n",
			m.keyExpr(), m.Name, inputArg)
	} else {
		fmt.Fprintf(buf, "\treturn restatec.Send(ctx, s.client, serviceName, %q, %s)\n", m.Name, inputArg)
	}

	buf.WriteString("}\n\n")
}

// writeFutureMethod emits a response-future method on the future proxy.
func writeFutureMethod(buf *bytes.Buffer, futureType string, m methodInfo, isWorkflow bool) {
	outType := "restate.Void"
	if m.HasOutput {
		outType = m.OutputType
	}
	fmt.Fprintf(buf, "func (f *%s) %s(%s) restate.ResponseFuture[%s] {\n",
		futureType, m.Name, m.params("rctx restate.Context"), outType)

	inputArg := "nil"
	if m.HasInput {
		inputArg = m.InputName
	}
	if isWorkflow {
		fmt.Fprintf(
			buf,
			"\treturn restate.Workflow[%s](rctx, serviceName, %s, %q).RequestFuture(%s)\n",
			outType,
			m.keyExpr(),
			m.Name,
			inputArg,
		)
	} else {
		fmt.Fprintf(
			buf,
			"\treturn restate.Service[%s](rctx, serviceName, %q).RequestFuture(%s)\n",
			outType,
			m.Name,
			inputArg,
		)
	}

	buf.WriteString("}\n\n")
}

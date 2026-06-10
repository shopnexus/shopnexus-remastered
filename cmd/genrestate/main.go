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
	// IsCommand is true when the first param is restate.Context (durable,
	// journaled command); false for context.Context (non-durable query).
	IsCommand bool
}

// ctxType renders the method's first-param ctx type as a parameter
// declaration, e.g. "ctx context.Context" or "ctx restate.Context".
func (m methodInfo) ctxType(name string) string {
	if m.IsCommand {
		return name + " restate.Context"
	}
	return name + " context.Context"
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

	proxyBase := strings.TrimSuffix(*proxyType, "Client")
	callType := proxyBase + "Call"     // restate command request-response proxy
	callIface := *ifaceName + "Call"   // command request-response surface
	senderType := proxyBase + "Sender" // restate command one-way proxy
	senderIface := *ifaceName + "Sender"
	futureType := proxyBase + "Future" // restate command future proxy
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

			// First param decides query vs command: context.Context → query
			// (non-durable, flat/besteffort); restate.Context → command
			// (durable, dispatched via the Restate proxy).
			if ft.Params.NumFields() > 0 {
				ctxStr := renderType(fset, ft.Params.List[0].Type, *srcPkg, samePackage, usedImports, d)
				m.IsCommand = ctxStr == "restate.Context"
			}

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

	// Partition by ctx type: queries (context.Context) → flat besteffort
	// surface; commands (restate.Context) → Call/Future/Sender Restate proxy.
	// Workflows are all-command (durable) by nature.
	var queries, commands []methodInfo
	for _, m := range methods {
		if isWorkflow || m.IsCommand {
			commands = append(commands, m)
		} else {
			queries = append(queries, m)
		}
	}

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
	if !isWorkflow {
		usedImports["besteffort"] = "shopnexus-server/internal/shared/besteffort"
		usedImports["json"] = "encoding/json"
	}

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

	if isWorkflow {
		writeWorkflowProxy(&buf, *ifaceName, ifaceRef, *proxyType, senderType, senderIface, futureType, futureIface, clientIface, methods)

		formatted, err := format.Source(buf.Bytes())
		if err != nil {
			log.Fatalf("gofmt: %v\nraw output:\n%s", err, buf.String())
		}
		if err := os.WriteFile(*outFile, formatted, 0644); err != nil {
			log.Fatalf("write %s: %v", *outFile, err)
		}
		fmt.Printf("genrestate: wrote %s (%d methods)\n", *outFile, len(methods))
		return
	}

	// Command surfaces (Call/Sender/Future) mirror the durable restate.Context
	// methods.

	// Call interface: command request-response, restate.Context.
	fmt.Fprintf(&buf, "// %s mirrors the durable (command) methods of %s as request-response calls.\n", callIface, ifaceRef)
	fmt.Fprintf(&buf, "type %s interface {\n", callIface)
	for _, m := range commands {
		if m.HasOutput {
			fmt.Fprintf(&buf, "\t%s(%s) (%s, error)\n", m.Name, m.params(m.ctxType("ctx")), m.OutputType)
		} else {
			fmt.Fprintf(&buf, "\t%s(%s) error\n", m.Name, m.params(m.ctxType("ctx")))
		}
	}
	buf.WriteString("}\n\n")

	// Sender interface: command one-way calls, outputs dropped.
	fmt.Fprintf(&buf, "// %s mirrors the command methods of %s as one-way (fire-and-forget) calls.\n", senderIface, ifaceRef)
	fmt.Fprintf(&buf, "type %s interface {\n", senderIface)
	for _, m := range commands {
		fmt.Fprintf(&buf, "\t%s(%s) error\n", m.Name, m.params(m.ctxType("ctx")))
	}
	buf.WriteString("}\n\n")

	// Future interface: command methods returning response futures.
	fmt.Fprintf(&buf, "// %s mirrors the command methods of %s returning response futures for\n// racing or parallel calls. Only usable inside a Restate handler context.\n", futureIface, ifaceRef)
	fmt.Fprintf(&buf, "type %s interface {\n", futureIface)
	for _, m := range commands {
		outType := "restate.Void"
		if m.HasOutput {
			outType = m.OutputType
		}
		fmt.Fprintf(&buf, "\t%s(%s) restate.ResponseFuture[%s]\n", m.Name, m.params("rctx restate.Context"), outType)
	}
	buf.WriteString("}\n\n")

	// Client interface: the cross-module DI type. Queries are flat inline
	// methods (non-durable besteffort transport); Call()/Future()/Send() expose
	// the durable command surfaces. Workflows have no queries.
	fmt.Fprintf(&buf, "// %s is the cross-module client: query methods are flat (non-durable),\n// Call()/Future()/Send() reach the durable command surfaces.\n", clientIface)
	fmt.Fprintf(&buf, "type %s interface {\n", clientIface)
	for _, m := range queries {
		if m.HasOutput {
			fmt.Fprintf(&buf, "\t%s(%s) (%s, error)\n", m.Name, m.params(m.ctxType("ctx")), m.OutputType)
		} else {
			fmt.Fprintf(&buf, "\t%s(%s) error\n", m.Name, m.params(m.ctxType("ctx")))
		}
	}
	fmt.Fprintf(&buf, "\tCall() %s\n\tFuture() %s\n\tSend() %s\n}\n\n", callIface, futureIface, senderIface)

	// Restate command proxy: request-response over HTTP ingress.
	fmt.Fprintf(&buf, "// %s implements %s via Restate HTTP ingress.\n", callType, callIface)
	fmt.Fprintf(&buf, "type %s struct {\n\tcall *restatec.CallClient\n}\n\n", callType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", callIface, callType)
	fmt.Fprintf(&buf, "func New%s(restateIngressURL string) *%s {\n", callType, callType)
	fmt.Fprintf(&buf, "\treturn &%s{call: restatec.NewCallClient(restateIngressURL)}\n}\n\n", callType)
	for _, m := range commands {
		writeCallMethod(&buf, callType, m, isWorkflow)
	}

	// One-way command proxy.
	fmt.Fprintf(&buf, "// %s implements %s.\n", senderType, senderIface)
	fmt.Fprintf(&buf, "type %s struct {\n\tclient *restatec.SendClient\n}\n\n", senderType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", senderIface, senderType)
	for _, m := range commands {
		writeSendMethod(&buf, senderType, m, isWorkflow)
	}

	// Future command proxy.
	fmt.Fprintf(&buf, "// %s implements %s via the Restate SDK.\n", futureType, futureIface)
	fmt.Fprintf(&buf, "type %s struct{}\n\n", futureType)
	fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n\n", futureIface, futureType)
	for _, m := range commands {
		writeFutureMethod(&buf, futureType, m, isWorkflow)
	}

	writeServiceAdapter(&buf, *serviceName, ifaceRef, methods)
	writeClient(&buf, clientArgs{
		ifaceName:   *ifaceName,
		ifaceRef:    ifaceRef,
		serviceName: *serviceName,
		clientIface: clientIface,
		callType:    callType,
		callIface:   callIface,
		senderType:  senderType,
		senderIface: senderIface,
		futureType:  futureType,
		futureIface: futureIface,
		queries:     queries,
	})

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
	fmt.Fprintf(buf, "func (p *%s) %s(%s) ", proxyType, m.Name, m.params(m.ctxType("ctx")))

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

// writeServiceAdapter emits the Guaranteed adapter: a struct wrapping the biz
// interface with restate.Context methods and ServiceName() for restate.Reflect.
func writeServiceAdapter(buf *bytes.Buffer, serviceName, ifaceRef string, methods []methodInfo) {
	adapter := serviceName + "Service"

	fmt.Fprintf(buf, "// %s adapts %s into restate.Context handlers for restate.Reflect.\n", adapter, ifaceRef)
	fmt.Fprintf(buf, "type %s struct {\n\tbiz %s\n}\n\n", adapter, ifaceRef)
	fmt.Fprintf(buf, "func New%s(biz %s) *%s { return &%s{biz: biz} }\n\n", adapter, ifaceRef, adapter, adapter)
	fmt.Fprintf(buf, "func (s *%s) ServiceName() string { return serviceName }\n\n", adapter)

	for _, m := range methods {
		fmt.Fprintf(buf, "func (s *%s) %s(%s) ", adapter, m.Name, m.params("ctx restate.Context"))
		if m.HasOutput {
			fmt.Fprintf(buf, "(%s, error)", m.OutputType)
		} else {
			buf.WriteString("error")
		}
		buf.WriteString(" {\n")

		args := "ctx"
		if m.HasInput {
			args += ", " + m.InputName
		}
		fmt.Fprintf(buf, "\treturn s.biz.%s(%s)\n}\n\n", m.Name, args)
	}
}

// lowerFirst lowercases the first rune of s (e.g. Catalog → catalog).
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// clientArgs bundles the names writeClient needs.
type clientArgs struct {
	ifaceName, ifaceRef, serviceName string
	clientIface                      string
	callType, callIface              string
	senderType, senderIface          string
	futureType, futureIface          string
	queries                          []methodInfo
}

// writeClient emits the flat-query client: a flat-impl interface over the query
// methods, in-process + HTTP/2 besteffort impls, the unified client struct
// (flat queries delegate to the flat impl; Call()/Future()/Send() return the
// command proxies), InProcess/Remote constructors, and the besteffort HTTP
// server registration (query methods only — commands are served by Restate).
// With zero queries every query loop is empty: empty flat interface, empty
// Register body, no flat methods — all valid Go.
func writeClient(buf *bytes.Buffer, a clientArgs) {
	lower := lowerFirst(a.ifaceName)
	flatIface := a.ifaceName + "Flat"
	localType := lower + "BestEffortLocal"
	remoteType := lower + "BestEffortRemote"
	clientType := lower + "Client"

	// Flat surface = the query methods, request-response, context.Context.
	fmt.Fprintf(buf, "// %s is the flat (non-durable query) surface of %s.\n", flatIface, a.ifaceRef)
	fmt.Fprintf(buf, "type %s interface {\n", flatIface)
	for _, m := range a.queries {
		fmt.Fprintf(buf, "\t%s\n", querySig(m))
	}
	buf.WriteString("}\n\n")

	// In-process flat: delegates queries to the biz directly.
	fmt.Fprintf(buf, "// %s delegates flat queries to the in-process biz.\n", localType)
	fmt.Fprintf(buf, "type %s struct{ biz %s }\n\n", localType, a.ifaceRef)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", flatIface, localType)
	for _, m := range a.queries {
		fmt.Fprintf(buf, "func (b *%s) %s {\n", localType, querySig(m))
		args := "ctx"
		if m.HasInput {
			args += ", " + m.InputName
		}
		fmt.Fprintf(buf, "\treturn b.biz.%s(%s)\n}\n\n", m.Name, args)
	}

	// HTTP/2 flat: posts JSON to the besteffort server.
	fmt.Fprintf(buf, "// %s routes flat queries over HTTP/2.\n", remoteType)
	fmt.Fprintf(buf, "type %s struct{ call *besteffort.CallClient }\n\n", remoteType)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", flatIface, remoteType)
	for _, m := range a.queries {
		fmt.Fprintf(buf, "func (b *%s) %s {\n", remoteType, querySig(m))
		inputArg := "nil"
		if m.HasInput {
			inputArg = m.InputName
		}
		if m.HasOutput {
			fmt.Fprintf(buf, "\treturn besteffort.Call[%s](ctx, b.call, serviceName, %q, %s)\n}\n\n",
				m.OutputType, m.Name, inputArg)
		} else {
			fmt.Fprintf(buf, "\treturn besteffort.CallVoid(ctx, b.call, serviceName, %q, %s)\n}\n\n",
				m.Name, inputArg)
		}
	}

	// Unified client: flat impl for queries + command proxies for Call/Future/Send.
	fmt.Fprintf(buf, "// %s holds the flat query impl and the durable command proxies.\n", clientType)
	fmt.Fprintf(buf, "type %s struct {\n\tflat %s\n\tcall *%s\n\tsend *%s\n\tfuture *%s\n}\n\n",
		clientType, flatIface, a.callType, a.senderType, a.futureType)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", a.clientIface, clientType)

	// Flat query methods delegate to the flat impl.
	for _, m := range a.queries {
		fmt.Fprintf(buf, "func (c *%s) %s {\n", clientType, querySig(m))
		args := "ctx"
		if m.HasInput {
			args += ", " + m.InputName
		}
		fmt.Fprintf(buf, "\treturn c.flat.%s(%s)\n}\n\n", m.Name, args)
	}

	fmt.Fprintf(buf, "func (c *%s) Call() %s { return c.call }\n\n", clientType, a.callIface)
	fmt.Fprintf(buf, "func (c *%s) Future() %s { return c.future }\n\n", clientType, a.futureIface)
	fmt.Fprintf(buf, "func (c *%s) Send() %s { return c.send }\n\n", clientType, a.senderIface)

	// commandProxies renders the shared call/send/future field initializers.
	commandProxies := fmt.Sprintf(
		"call:   New%s(restateIngressURL),\n\t\tsend:   &%s{client: restatec.NewSendClient(restateIngressURL)},\n\t\tfuture: &%s{},",
		a.callType, a.senderType, a.futureType)

	// In-process variant (monolith): flat queries hit the biz directly.
	fmt.Fprintf(buf, "// New%sInProcess builds a client whose flat queries call the in-process biz.\n", a.clientIface)
	fmt.Fprintf(buf, "func New%sInProcess(restateIngressURL string, biz %s) %s {\n", a.clientIface, a.ifaceRef, a.clientIface)
	fmt.Fprintf(buf, "\treturn &%s{\n\t\tflat: &%s{biz: biz},\n\t\t%s\n\t}\n}\n\n", clientType, localType, commandProxies)

	// Remote variant (split): flat queries over HTTP/2.
	fmt.Fprintf(buf, "// New%sRemote builds a client whose flat queries call a remote besteffort server.\n", a.clientIface)
	fmt.Fprintf(buf, "func New%sRemote(restateIngressURL, bestEffortURL string) %s {\n", a.clientIface, a.clientIface)
	fmt.Fprintf(buf, "\treturn &%s{\n\t\tflat: &%s{call: besteffort.NewCallClient(bestEffortURL)},\n\t\t%s\n\t}\n}\n\n", clientType, remoteType, commandProxies)

	// BestEffort HTTP server registration: query methods only.
	fmt.Fprintf(buf, "// Register%sBestEffort wires the query methods onto a besteffort HTTP server.\n// Commands are served by Restate, not here.\n", a.serviceName)
	fmt.Fprintf(buf, "func Register%sBestEffort(s *besteffort.Server, biz %s) {\n", a.serviceName, a.ifaceRef)
	for _, m := range a.queries {
		fmt.Fprintf(buf, "\ts.Handle(serviceName, %q, func(ctx context.Context, body []byte) (any, error) {\n", m.Name)
		callArgs := "ctx"
		if m.HasInput {
			fmt.Fprintf(buf, "\t\tvar p %s\n", m.InputType)
			buf.WriteString("\t\tif err := json.Unmarshal(body, &p); err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
			callArgs += ", p"
		}
		if m.HasOutput {
			fmt.Fprintf(buf, "\t\treturn biz.%s(%s)\n", m.Name, callArgs)
		} else {
			fmt.Fprintf(buf, "\t\treturn nil, biz.%s(%s)\n", m.Name, callArgs)
		}
		buf.WriteString("\t})\n")
	}
	buf.WriteString("}\n\n")
}

// writeWorkflowProxy emits the legacy workflow client: a Restate proxy over
// all methods (request-response, one-way Send, response Future) keyed by the
// workflow-key param. Workflows are wholly durable, so there is no flat/
// besteffort surface — this preserves the pre-split workflow output.
func writeWorkflowProxy(
	buf *bytes.Buffer,
	ifaceName, ifaceRef, proxyType, senderType, senderIface, futureType, futureIface, clientIface string,
	methods []methodInfo,
) {
	// Sender interface: every method as a one-way call, outputs dropped.
	fmt.Fprintf(buf, "// %s mirrors %s as one-way (fire-and-forget) calls; outputs are dropped.\n", senderIface, ifaceRef)
	fmt.Fprintf(buf, "type %s interface {\n", senderIface)
	for _, m := range methods {
		fmt.Fprintf(buf, "\t%s(%s) error\n", m.Name, m.params("ctx context.Context"))
	}
	buf.WriteString("}\n\n")

	// Future interface: every method returns a ResponseFuture for racing/parallel calls.
	fmt.Fprintf(buf, "// %s mirrors %s returning response futures for racing\n// or parallel calls. Only usable inside a Restate handler context.\n", futureIface, ifaceRef)
	fmt.Fprintf(buf, "type %s interface {\n", futureIface)
	for _, m := range methods {
		outType := "restate.Void"
		if m.HasOutput {
			outType = m.OutputType
		}
		fmt.Fprintf(buf, "\t%s(%s) restate.ResponseFuture[%s]\n", m.Name, m.params("rctx restate.Context"), outType)
	}
	buf.WriteString("}\n\n")

	// Client interface: embeds the biz surface + Send/Future.
	fmt.Fprintf(buf, "// %s is the cross-module client: direct methods are request-response, Send() is one-way, Future() returns response futures.\n", clientIface)
	fmt.Fprintf(buf, "type %s interface {\n\t%s\n\tSend() %s\n\tFuture() %s\n}\n\n", clientIface, ifaceRef, senderIface, futureIface)

	// Request-response proxy.
	fmt.Fprintf(buf, "// %s implements %s via Restate HTTP ingress.\n", proxyType, clientIface)
	fmt.Fprintf(buf, "type %s struct {\n\tcall *restatec.CallClient\n\tsend *%s\n\tfuture *%s\n}\n\n", proxyType, senderType, futureType)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", clientIface, proxyType)
	fmt.Fprintf(buf, "func New%s(restateIngressURL string) *%s {\n", proxyType, proxyType)
	fmt.Fprintf(buf, "\treturn &%s{\n\t\tcall: restatec.NewCallClient(restateIngressURL),\n\t\tsend: &%s{client: restatec.NewSendClient(restateIngressURL)},\n\t\tfuture: &%s{},\n\t}\n}\n\n", proxyType, senderType, futureType)
	fmt.Fprintf(buf, "func (p *%s) Send() %s { return p.send }\n\n", proxyType, senderIface)
	fmt.Fprintf(buf, "func (p *%s) Future() %s { return p.future }\n\n", proxyType, futureIface)
	for _, m := range methods {
		writeCallMethod(buf, proxyType, m, true)
	}

	// One-way proxy.
	fmt.Fprintf(buf, "// %s implements %s.\n", senderType, senderIface)
	fmt.Fprintf(buf, "type %s struct {\n\tclient *restatec.SendClient\n}\n\n", senderType)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", senderIface, senderType)
	for _, m := range methods {
		writeSendMethod(buf, senderType, m, true)
	}

	// Future proxy.
	fmt.Fprintf(buf, "// %s implements %s via the Restate SDK.\n", futureType, futureIface)
	fmt.Fprintf(buf, "type %s struct{}\n\n", futureType)
	fmt.Fprintf(buf, "var _ %s = (*%s)(nil)\n\n", futureIface, futureType)
	for _, m := range methods {
		writeFutureMethod(buf, futureType, m, true)
	}
}

// querySig renders a query method's full signature (name + params + results),
// always with context.Context as the first param.
func querySig(m methodInfo) string {
	sig := m.Name + "(" + m.params("ctx context.Context") + ") "
	if m.HasOutput {
		return sig + "(" + m.OutputType + ", error)"
	}
	return sig + "error"
}

// writeSendMethod emits a one-way method on the sender proxy; outputs are dropped.
func writeSendMethod(buf *bytes.Buffer, senderType string, m methodInfo, isWorkflow bool) {
	fmt.Fprintf(buf, "func (s *%s) %s(%s) error {\n", senderType, m.Name, m.params(m.ctxType("ctx")))

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

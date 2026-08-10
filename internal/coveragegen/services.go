package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ifaceDecl is a parsed interface type declaration.
type ifaceDecl struct {
	name    string
	doc     string
	methods []Operation
}

// collectServices walks services/<name>/driver and builds a Service per driver package.
func collectServices(servicesDir string) (map[string]*Service, error) {
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		return nil, err
	}

	out := make(map[string]*Service)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		driverDir := filepath.Join(servicesDir, e.Name(), "driver")
		if _, statErr := os.Stat(driverDir); statErr != nil {
			continue
		}

		svc, buildErr := buildService(servicesDir, e.Name(), driverDir)
		if buildErr != nil {
			return nil, buildErr
		}

		if svc != nil {
			out[e.Name()] = svc
		}
	}

	return out, nil
}

func buildService(servicesDir, name, driverDir string) (*Service, error) {
	ifaces, err := parseInterfaces(driverDir)
	if err != nil {
		return nil, err
	}

	if len(ifaces) == 0 {
		return nil, nil
	}

	primaryName := primaryInterface(filepath.Join(servicesDir, name), ifaces)
	svc := &Service{
		Name:      name,
		Interface: primaryName,
		Providers: map[string]string{},
	}

	for _, iface := range ifaces {
		if iface.name == primaryName {
			svc.Operations = iface.methods
			continue
		}

		svc.Capabilities = append(svc.Capabilities, Capability{
			Name:       iface.name,
			Doc:        iface.doc,
			Operations: iface.methods,
		})
	}

	sort.Slice(svc.Capabilities, func(i, j int) bool { return svc.Capabilities[i].Name < svc.Capabilities[j].Name })

	return svc, nil
}

// rawIface holds an interface type plus its doc before embed-flattening.
type rawIface struct {
	typ *ast.InterfaceType
	doc string
}

// parseInterfaces returns every exported interface declared in a package dir,
// with embedded interfaces (exported or not, same package) flattened into a
// single operation list.
func parseInterfaces(dir string) ([]ifaceDecl, error) {
	fset := token.NewFileSet()

	//nolint:staticcheck // ParseDir is adequate here; build-tag precision is unneeded for docs generation.
	pkgs, err := parser.ParseDir(fset, dir, notTest, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	all := map[string]rawIface{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			collectRawInterfaces(file, all)
		}
	}

	var out []ifaceDecl

	for name, raw := range all {
		if !ast.IsExported(name) {
			continue
		}

		out = append(out, ifaceDecl{
			name:    name,
			doc:     firstLine(raw.doc),
			methods: flattenMethods(name, all, map[string]bool{}),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out, nil
}

func collectRawInterfaces(file *ast.File, all map[string]rawIface) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}

		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}

			all[ts.Name.Name] = rawIface{typ: it, doc: declDoc(gd, ts)}
		}
	}
}

// flattenMethods returns the deduped, sorted operations of an interface,
// recursively pulling in methods from same-package embedded interfaces.
func flattenMethods(name string, all map[string]rawIface, seen map[string]bool) []Operation {
	if seen[name] {
		return nil
	}

	seen[name] = true

	raw, ok := all[name]
	if !ok {
		return nil
	}

	byName := map[string]Operation{}

	for _, field := range raw.typ.Methods.List {
		if len(field.Names) == 0 { // embedded interface
			if id, isIdent := field.Type.(*ast.Ident); isIdent {
				for _, op := range flattenMethods(id.Name, all, seen) {
					byName[op.Name] = op
				}
			}

			continue
		}

		for _, n := range field.Names {
			if n.IsExported() {
				byName[n.Name] = Operation{Name: n.Name, Doc: firstLine(commentText(field.Doc))}
			}
		}
	}

	ops := make([]Operation, 0, len(byName))
	for _, op := range byName {
		ops = append(ops, op)
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })

	return ops
}

// primaryInterface picks the interface the portable package actually stores as
// its driver (referenced as `driver.<Name>`); falls back to the largest interface.
func primaryInterface(portableDir string, ifaces []ifaceDecl) string {
	if ref := referencedInterface(portableDir, ifaces); ref != "" {
		return ref
	}

	largest := ifaces[0].name
	for _, i := range ifaces {
		if len(i.methods) > lenMethods(ifaces, largest) {
			largest = i.name
		}
	}

	return largest
}

func lenMethods(ifaces []ifaceDecl, name string) int {
	for _, i := range ifaces {
		if i.name == name {
			return len(i.methods)
		}
	}

	return 0
}

// referencedInterface returns the driver interface the portable package holds as
// its backend — the one used as a struct-field or parameter type (e.g.
// `driver driver.Networking`), the most-referenced such type. Only type
// positions count, so an optional capability a wrapper probes for via a type
// assertion (`x.(driver.NetworkInterfaces)`) is never mistaken for the primary.
func referencedInterface(portableDir string, ifaces []ifaceDecl) string {
	fset := token.NewFileSet()

	//nolint:staticcheck // ParseDir is adequate here; build-tag precision is unneeded for docs generation.
	pkgs, err := parser.ParseDir(fset, portableDir, notTest, 0)
	if err != nil {
		return ""
	}

	known := map[string]int{}
	for _, i := range ifaces {
		known[i.name] = 0
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			countFieldTypes(file, known)
		}
	}

	best, bestN := "", 0
	for name, n := range known {
		if n > bestN {
			best, bestN = name, n
		}
	}

	return best
}

// countFieldTypes counts, per known interface, how often it is the type of a
// struct field, function parameter, or result (every ast.Field carries a type),
// i.e. where the package stores or consumes the interface — never a type
// assertion, which is an expression, not a field.
func countFieldTypes(file *ast.File, known map[string]int) {
	ast.Inspect(file, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok {
			return true
		}

		if name := selectorType(field.Type); name != "" {
			if _, tracked := known[name]; tracked {
				known[name]++
			}
		}

		return true
	})
}

// selectorType returns the selector name for a `pkg.Name` type expression,
// unwrapping a leading pointer, else "".
func selectorType(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	return sel.Sel.Name
}

func notTest(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }

func declDoc(gd *ast.GenDecl, ts *ast.TypeSpec) string {
	if ts.Doc != nil {
		return commentText(ts.Doc)
	}

	return commentText(gd.Doc)
}

func commentText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}

	return strings.TrimSpace(cg.Text())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}

	return strings.TrimSpace(s)
}

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

// providerNativeInterface marks a Service that has no portable driver interface
// under services/*/driver: it is a provider-native wire handler, discovered from
// the provider's HTTP server, with operations taken from its backing mock.
const providerNativeInterface = "provider-native"

// synthesizeNativeServices augments services with the provider-native wire
// services each provider's HTTP server registers but that no portable driver
// interface represents (EKS/AKS/GKE, STS, Redshift, the Azure ARM-infra
// handlers, GCP LRO/servicenetworking/cloudasset, OCI work requests, …).
//
// Without this, a service implemented only as a wire handler plus a provider
// mock — no services/<name>/driver interface — is invisible in docs/coverage,
// so llms.txt readers wrongly conclude it is unsupported. Everything here is
// still derived from the code (the server factory's registrations and the
// mock's method set), so it cannot drift.
func synthesizeNativeServices(root string, services map[string]*Service) error {
	for _, prov := range providerOrder {
		if err := synthesizeProviderNatives(root, prov, services); err != nil {
			return err
		}
	}

	return nil
}

func synthesizeProviderNatives(root, prov string, services map[string]*Service) error {
	credited := creditedNatives(prov, services)

	fields, err := providerStructFields(root, prov)
	if err != nil {
		return err
	}

	canonical, displayByPkg := providerHandlerIndex(fields, credited)

	registered, err := registeredHandlerPkgs(root, prov)
	if err != nil {
		return err
	}

	for _, pkg := range sortedKeys(registered) {
		if canonical[pkg] {
			continue
		}

		addNativeService(root, prov, pkg, displayByPkg[pkg], services)
	}

	return nil
}

// addNativeService inserts one provider-native Service for handler package pkg.
func addNativeService(root, prov, pkg, display string, services map[string]*Service) {
	if display == "" {
		display = displayName(pkg)
	}

	name := pkg
	if _, taken := services[name]; taken {
		name = pkg + "-" + prov
	}

	svc := &Service{
		Name:       name,
		Interface:  providerNativeInterface,
		Providers:  map[string]string{prov: display},
		Operations: []Operation{},
	}

	mockDir := filepath.Join(root, "providers", prov, pkg)
	if isDir(mockDir) {
		svc.Operations = nativeOperations(mockDir, services)
	}

	services[name] = svc
}

// creditedNatives returns the set of native names a provider already exposes
// through the portable services (Service.Providers[prov]).
func creditedNatives(prov string, services map[string]*Service) map[string]bool {
	out := map[string]bool{}

	for _, svc := range services {
		if n := svc.Providers[prov]; n != "" {
			out[n] = true
		}
	}

	return out
}

// provField is one field of a provider factory's Provider struct.
type provField struct {
	name   string // Go field name (the native display, e.g. EKS)
	pkg    string // last segment of the field type's import path
	isMock bool   // field typed as *pkg.Mock (vs a bare driver interface)
}

// providerStructFields parses providers/<prov>/<prov>.go and returns the fields
// of its Provider struct that carry a package-qualified type.
func providerStructFields(root, prov string) ([]provField, error) {
	factory := filepath.Join(root, "providers", prov, prov+".go")

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, factory, nil, 0)
	if err != nil {
		return nil, err
	}

	aliases := importAliases(file)

	var out []provField

	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != providerStructName {
			return true
		}

		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}

		for _, f := range st.Fields.List {
			if pf, ok := parseProvField(f, aliases); ok {
				out = append(out, pf)
			}
		}

		return false
	})

	return out, nil
}

func parseProvField(f *ast.Field, aliases map[string]string) (provField, bool) {
	if len(f.Names) == 0 {
		return provField{}, false
	}

	expr := f.Type

	star, isPtr := expr.(*ast.StarExpr)
	if isPtr {
		expr = star.X
	}

	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return provField{}, false
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return provField{}, false
	}

	return provField{
		name:   f.Names[0].Name,
		pkg:    lastSegment(aliases[id.Name]),
		isMock: isPtr,
	}, true
}

// providerHandlerIndex derives, from the Provider struct fields:
//   - canonical: handler package names already surfaced as a portable service's
//     native for this provider (so their handlers are not re-synthesized). A
//     credited field contributes both its mock package (branded names like ELB's
//     elbv2, and to distinguish siblings such as RDS vs Redshift) and its
//     lowercased field name (for providers whose factory holds bare driver
//     interfaces, e.g. OCI).
//   - displayByPkg: mock-package -> field name, so a synthesized service takes a
//     proper native display name (eks -> EKS, cloudsql -> CloudSQL).
func providerHandlerIndex(
	fields []provField, credited map[string]bool,
) (canonical map[string]bool, displayByPkg map[string]string) {
	canonical = map[string]bool{}
	displayByPkg = map[string]string{}

	for _, f := range fields {
		if f.isMock && f.pkg != "" {
			displayByPkg[f.pkg] = f.name
		}

		if !credited[f.name] {
			continue
		}

		canonical[strings.ToLower(f.name)] = true

		if f.isMock && f.pkg != "" {
			canonical[f.pkg] = true
		}
	}

	return canonical, displayByPkg
}

// registeredHandlerPkgs returns the top-level handler packages under
// server/<prov>/ that the provider's server New() constructs (any pkg.NewXxx
// call). Handlers registered inside helper functions are intentionally omitted:
// they are sub-components of a service already counted (e.g. Databricks
// data-plane handlers), not distinct services.
func registeredHandlerPkgs(root, prov string) (map[string]bool, error) {
	factory := filepath.Join(root, "server", prov, prov+".go")

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, factory, nil, 0)
	if err != nil {
		return nil, err
	}

	aliases := importAliases(file)
	out := map[string]bool{}

	fn := findFunc(file, "New")
	if fn == nil || fn.Body == nil {
		return out, nil
	}

	marker := "/server/" + prov + "/"

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if pkg := handlerCallPkg(n, aliases, marker); pkg != "" {
			out[pkg] = true
		}

		return true
	})

	return out, nil
}

// handlerCallPkg returns the top-level handler package for a `pkg.NewXxx(...)`
// call whose package import path sits under marker, else "".
func handlerCallPkg(n ast.Node, aliases map[string]string, marker string) string {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return ""
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "New") {
		return ""
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}

	path, ok := aliases[id.Name]
	if !ok {
		return ""
	}

	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}

	rest := path[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}

	return rest
}

// nativeOperations returns the operations for a provider-native service backed
// by the mock in mockDir. A mock that fully implements a portable driver
// interface (a sibling service such as RDS/SQL/CloudSQL sharing the relationaldb
// driver) reports that interface's operations, so the surface matches the driver
// rather than the mock's internal helpers. Otherwise — the mock has no portable
// counterpart (EKS/AKS/GKE) — it reports the mock's own exported methods, minus
// the persistence and wiring plumbing (Snapshot/Restore and Set* setters).
func nativeOperations(mockDir string, services map[string]*Service) []Operation {
	if svc := implementedService(mockDir, services); svc != nil {
		return svc.Operations
	}

	methods := mockMethods(mockDir)

	ops := make([]Operation, 0, len(methods))

	for name := range methods {
		if isNativeOperation(name) {
			ops = append(ops, Operation{Name: name})
		}
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })

	return ops
}

func isNativeOperation(name string) bool {
	if !ast.IsExported(name) {
		return false
	}

	switch name {
	case "Snapshot", "Restore":
		return false
	}

	return !strings.HasPrefix(name, "Set")
}

// displayName is the fallback native display for a handler package with no
// dedicated provider mock field, upper-casing known acronyms and otherwise
// title-casing the package name.
func displayName(pkg string) string {
	switch pkg {
	case "sts":
		return "STS"
	case "lro":
		return "LRO"
	}

	if pkg == "" {
		return ""
	}

	return strings.ToUpper(pkg[:1]) + pkg[1:]
}

func lastSegment(path string) string {
	if path == "" {
		return ""
	}

	return path[strings.LastIndex(path, "/")+1:]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

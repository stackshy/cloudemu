package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// providerStructName is the factory struct every provider package declares.
const providerStructName = "Provider"

// attachProviders reads each provider factory struct and records, per service,
// the native field name each provider exposes it under (e.g. storage -> S3).
func attachProviders(root string, services map[string]*Service) error {
	byName := map[string]*Service{}
	for _, svc := range services {
		byName[svc.Name] = svc
	}

	for _, prov := range providerOrder {
		if err := attachProvider(root, prov, byName); err != nil {
			return err
		}
	}

	return nil
}

func attachProvider(root, prov string, byName map[string]*Service) error {
	factory := filepath.Join(root, "providers", prov, prov+".go")
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, factory, nil, 0)
	if err != nil {
		return err
	}

	aliases := importAliases(file)
	built := constructedFields(file)

	for _, field := range providerFields(file) {
		pkgPath, ok := aliases[field.pkgAlias]
		if !ok || !built[field.name] {
			continue
		}

		if svc := resolveField(root, prov, pkgPath, byName); svc != nil {
			svc.Providers[prov] = field.name
		}
	}

	return nil
}

// constructedFields returns the Provider fields the factory actually populates:
// keys set to a non-nil value in the `&Provider{...}` literal, or assigned via
// `p.<Field> = ...`. A field merely declared on the struct (OCI's nil service
// slots) is not counted, so the docs never claim an unimplemented service.
func constructedFields(file *ast.File) map[string]bool {
	out := map[string]bool{}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			collectLiteralFields(node, out)
		case *ast.AssignStmt:
			collectAssignedFields(node, out)
		}

		return true
	})

	return out
}

func collectLiteralFields(cl *ast.CompositeLit, out map[string]bool) {
	if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != providerStructName {
		return
	}

	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		if key, ok := kv.Key.(*ast.Ident); ok && !isNilIdent(kv.Value) {
			out[key.Name] = true
		}
	}
}

func collectAssignedFields(as *ast.AssignStmt, out map[string]bool) {
	for i, lhs := range as.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok {
			continue
		}

		if _, ok := sel.X.(*ast.Ident); !ok {
			continue
		}

		if i < len(as.Rhs) && isNilIdent(as.Rhs[i]) {
			continue
		}

		out[sel.Sel.Name] = true
	}
}

func isNilIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "nil"
}

// resolveField maps a factory field's package to a service. A field typed as a
// driver interface directly (`svc.Bucket`, the OCI style) resolves by import
// path; a field typed as a provider mock (`*s3.Mock`) resolves by method-set.
func resolveField(root, prov, pkgPath string, byName map[string]*Service) *Service {
	if svcName, ok := driverServiceName(pkgPath); ok {
		return byName[svcName]
	}

	if !strings.Contains(pkgPath, "/providers/"+prov+"/") {
		return nil
	}

	mockDir := filepath.Join(root, strings.TrimPrefix(pkgPath, modulePath+"/"))

	return implementedService(mockDir, byName)
}

// driverServiceName maps ".../services/<svc>/driver" to <svc>.
func driverServiceName(path string) (string, bool) {
	const marker = "/services/"

	i := strings.Index(path, marker)
	if i < 0 || !strings.HasSuffix(path, "/driver") {
		return "", false
	}

	rest := path[i+len(marker) : len(path)-len("/driver")]
	if strings.Contains(rest, "/") {
		return "", false
	}

	return rest, true
}

type structField struct {
	name     string
	pkgAlias string
}

// providerFields returns the named fields of the `Provider` struct whose type is
// a pointer to another package's type (`*pkg.Mock`), carrying the field name and
// that package's local alias.
func providerFields(file *ast.File) []structField {
	return structFields(file, providerStructName)
}

// structFields returns the named fields of the top-level struct type typeName
// whose type is a package-selector expression (`pkg.Type` or `*pkg.Type`),
// carrying the field name and that package's local alias. Shared by
// providerFields (typeName "Provider") and the registration cross-check
// (typeName "Drivers").
func structFields(file *ast.File, typeName string) []structField {
	var out []structField

	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typeName {
			return true
		}

		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}

		for _, f := range st.Fields.List {
			alias := selectorPkg(f.Type)
			if alias == "" || len(f.Names) == 0 {
				continue
			}

			out = append(out, structField{name: f.Names[0].Name, pkgAlias: alias})
		}

		return false
	})

	return out
}

// selectorPkg extracts `pkg` from a `*pkg.Type` (provider-mock) or `pkg.Type`
// (driver-interface) field type, else "".
func selectorPkg(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}

	return id.Name
}

func importAliases(file *ast.File) map[string]string {
	out := map[string]string{}

	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)

		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			name = path[strings.LastIndex(path, "/")+1:]
		}

		out[name] = path
	}

	return out
}

// implementedService returns the service whose primary interface the mock
// package actually implements — the service with the most operations fully
// covered by the mock's method set. A package that merely consumes another
// service's driver (e.g. EKS emitting metrics) will not cover that service's
// full interface, so it is not misattributed.
func implementedService(mockDir string, byName map[string]*Service) *Service {
	methods := mockMethods(mockDir)
	if len(methods) == 0 {
		return nil
	}

	var best *Service

	for _, svc := range byName {
		if len(svc.Operations) == 0 || !covers(methods, svc.Operations) {
			continue
		}

		if best == nil || len(svc.Operations) > len(best.Operations) {
			best = svc
		}
	}

	return best
}

// mockMethods collects the names of every method (func with a receiver)
// declared in a provider mock package.
func mockMethods(mockDir string) map[string]bool {
	fset := token.NewFileSet()

	//nolint:staticcheck // ParseDir is adequate here; build-tag precision is unneeded for docs generation.
	pkgs, err := parser.ParseDir(fset, mockDir, notTest, 0)
	if err != nil {
		return nil
	}

	out := map[string]bool{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Recv != nil && len(fn.Recv.List) > 0 {
					out[fn.Name.Name] = true
				}
			}
		}
	}

	return out
}

func covers(methods map[string]bool, ops []Operation) bool {
	for _, op := range ops {
		if !methods[op.Name] {
			return false
		}
	}

	return true
}

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
)

// driversStructName is the driver-bundle struct every server/<cloud>/<cloud>.go
// declares and passes to New.
const driversStructName = "Drivers"

// checkRegistrations cross-references, for every provider, the services its
// Provider factory actually implements (Service.Providers, populated by
// attachProviders) against the driver-typed fields server/<cloud>/<cloud>.go's
// Drivers struct declares AND reads inside New() — i.e. actually wires to a
// registered handler.
//
// A service with a driver interface and a populated provider field but no
// such field is a dead/absent wire handler: the backend implements the
// service in full, but no protocol on that cloud's wire server can ever reach
// it. This never fails the generator run; it returns human-readable warnings
// for the caller to surface.
func checkRegistrations(root string, services []*Service) ([]string, error) {
	var warnings []string

	for _, prov := range providerOrder {
		registered, err := registeredServices(root, prov)
		if err != nil {
			return nil, err
		}

		for _, svc := range services {
			// Provider-native services are, by construction, discovered from the
			// wire server's own registrations, so a dead-handler check is moot.
			if svc.Interface == providerNativeInterface {
				continue
			}

			native := svc.Providers[prov]
			if native == "" {
				continue
			}

			if !registered[svc.Name] {
				warnings = append(warnings, fmt.Sprintf(
					"coveragegen: %s implements service %q (as %s.%s) but server/%s/%s.go registers no wire handler for it",
					prov, svc.Name, prov, native, prov, prov))
			}
		}
	}

	return warnings, nil
}

// registeredServices returns the services a provider's wire server actually
// registers a handler for: the set of services backing a Drivers struct field
// that New() reads from its Drivers parameter.
func registeredServices(root, prov string) (map[string]bool, error) {
	factory := filepath.Join(root, "server", prov, prov+".go")

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, factory, nil, 0)
	if err != nil {
		return nil, err
	}

	aliases := importAliases(file)
	used := usedDriversFields(file)

	out := map[string]bool{}

	for _, field := range structFields(file, driversStructName) {
		if !used[field.name] {
			continue
		}

		pkgPath, ok := aliases[field.pkgAlias]
		if !ok {
			continue
		}

		if svcName, ok := driverServiceName(pkgPath); ok {
			out[svcName] = true
		}
	}

	return out, nil
}

// usedDriversFields returns the field names referenced as `<param>.Field`
// anywhere in New's body, where <param> is New's by-value Drivers parameter.
// A field only declared on Drivers but never read in New backs no handler.
func usedDriversFields(file *ast.File) map[string]bool {
	out := map[string]bool{}

	fn := findFunc(file, "New")
	if fn == nil || fn.Body == nil {
		return out
	}

	param := driversParamName(fn)
	if param == "" {
		return out
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if id, isIdent := sel.X.(*ast.Ident); isIdent && id.Name == param {
			out[sel.Sel.Name] = true
		}

		return true
	})

	return out
}

// findFunc returns the top-level, non-method function declaration named
// name, or nil.
func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}

	return nil
}

// driversParamName returns the name of fn's by-value `Drivers` parameter, or
// "" if it has none.
func driversParamName(fn *ast.FuncDecl) string {
	if fn.Type.Params == nil {
		return ""
	}

	for _, field := range fn.Type.Params.List {
		id, ok := field.Type.(*ast.Ident)
		if !ok || id.Name != driversStructName || len(field.Names) == 0 {
			continue
		}

		return field.Names[0].Name
	}

	return ""
}

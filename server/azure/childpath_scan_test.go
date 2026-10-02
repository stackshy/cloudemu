package azure_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// failClosedReason marks packages whose handlers already reject every child
// path they do not route (404, 405 or 501), confirmed by the e6810314 probes.
const failClosedReason = "rejects unrouted child paths already"

// childPathNoChildren lists the ParsePath packages that have no row in
// childPathRows, each with the reason. Known limit: the scan works per
// package, so a new resource type added to a package that already has a row
// is not caught here; the reviewer checks that the row table gained it.
func childPathNoChildren() map[string]string {
	noChildren := map[string]string{
		"storageaccount": "guarded together with the fileServices read every azurerm_storage_account " +
			"refresh depends on (W1 PR-C)",
	}

	for _, pkg := range []string{
		"apimanagement", "appconfiguration", "applicationgateway", "bastion", "batch", "cache",
		"communication", "containerinstances", "cosmosaccount", "cosmosdb", "databricks", "datafactory",
		"disks", "elasticsan", "firewall", "frontdoor", "healthcareapis", "iothub", "logic", "managedlustre",
		"mongocluster", "notificationhubs", "purview", "redisenterprise", "signalr", "streamanalytics",
		"virtualmachines", "webpubsub",
	} {
		noChildren[pkg] = failClosedReason
	}

	return noChildren
}

// parsePathPackages returns the server/azure subpackages whose non-test files
// call azurearm.ParsePath.
func parsePathPackages(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	fset := token.NewFileSet()

	for _, f := range files {
		pkg := filepath.Dir(f)
		if strings.HasSuffix(f, "_test.go") || seen[pkg] {
			continue
		}

		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ParsePath" {
				return true
			}

			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "azurearm" {
				seen[pkg] = true
			}

			return true
		})
	}

	out := make([]string, 0, len(seen))
	for pkg := range seen {
		out = append(out, pkg)
	}

	sort.Strings(out)

	return out
}

// TestChildPathRowsCoverEveryParsePathPackage is the drift guard: a new
// handler package that parses ARM paths must get a child-path row (and so a
// guard) or a stated reason it needs none.
func TestChildPathRowsCoverEveryParsePathPackage(t *testing.T) {
	rows := map[string]bool{}
	for _, row := range childPathRows() {
		rows[row.pkg] = true
	}

	noChildren := childPathNoChildren()
	scanned := parsePathPackages(t)

	if len(scanned) == 0 {
		t.Fatal("scan found no package calling azurearm.ParsePath")
	}

	for _, pkg := range scanned {
		if !rows[pkg] && noChildren[pkg] == "" {
			t.Errorf("package %s calls azurearm.ParsePath but has no child-path row", pkg)
		}
	}

	found := map[string]bool{}
	for _, pkg := range scanned {
		found[pkg] = true
	}

	for pkg := range rows {
		if !found[pkg] {
			t.Errorf("child-path row for %s, which no longer calls azurearm.ParsePath", pkg)
		}
	}

	for pkg := range noChildren {
		if !found[pkg] {
			t.Errorf("childPathNoChildren lists %s, which no longer calls azurearm.ParsePath", pkg)
		}
	}
}

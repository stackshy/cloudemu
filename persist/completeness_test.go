package persist_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// maxStoreScanDepth bounds the transitive struct walk holdsStore performs. It
// matches the depth used when the persistence scope was computed by reflection,
// so this guard's criterion is the same one that enumerated the services to
// implement: a provider field is "stateful" iff a *memstore.Store is reachable
// from its type within this many nested struct/pointer hops.
const maxStoreScanDepth = 4

// guardExclude lists provider fields (keyed "provider/Field") that transitively
// hold a *memstore.Store but are deliberately NOT persisted. It MUST stay empty
// unless a field genuinely holds only transient/derived state that must not
// survive a restart, and then only WITH a comment here justifying why. It is
// not an escape hatch for unfinished persistence work: a real stateful field
// belongs behind snapshot.Snapshottable, not in this list.
//
//nolint:gochecknoglobals // test fixture: an intentional, reviewed exclusion set.
var guardExclude = map[string]struct{}{}

// TestSnapshotCompleteness is the permanent completeness guard for #582: every
// provider field that transitively holds a *memstore.Store must implement
// snapshot.Snapshottable, or a stop/start of the server silently drops that
// service's state. It reflects over the concrete provider structs (NOT over
// Discover) so a NEW stateful service that forgets to add persistence fails
// here, naming the field, until it is implemented or justified in guardExclude.
//
// OCI is covered too, even though its Provider exposes services as DRIVER
// INTERFACES (netdriver.Networking, iamdriver.IAM, ...) rather than concrete
// *Mock structs: for a non-nil interface-typed field the guard resolves the
// runtime concrete type behind it (e.g. *identity.Mock) and runs the same
// holdsStore + Snapshottable check on that concrete type. So a stateful mock
// hidden behind an interface is caught, not blind-spotted. The AWS/Azure/GCP
// providers expose concrete *Mock fields, which take the static-type path.
func TestSnapshotCompleteness(t *testing.T) {
	snapType := reflect.TypeOf((*snapshot.Snapshottable)(nil)).Elem()

	providers := []struct {
		name string
		p    any
	}{
		{"aws", cloudemu.NewAWS()},
		{"azure", cloudemu.NewAzure()},
		{"gcp", cloudemu.NewGCP()},
		{"oci", cloudemu.NewOCI()},
	}

	missing := 0

	for _, prov := range providers {
		t.Run(prov.name, func(t *testing.T) {
			v := reflect.ValueOf(prov.p)
			for v.Kind() == reflect.Pointer {
				v = v.Elem()
			}

			if v.Kind() != reflect.Struct {
				t.Fatalf("%s: provider is not a struct", prov.name)
			}

			pt := v.Type()

			for i := range pt.NumField() {
				f := pt.Field(i)
				if !f.IsExported() {
					continue
				}

				// Resolve an interface-typed field (every OCI service field) to
				// the concrete type of the value it holds, so a stateful mock
				// behind the interface is inspected like a concrete *Mock field.
				// A nil interface holds no state and is skipped.
				ft := f.Type
				if ft.Kind() == reflect.Interface {
					fv := v.Field(i)
					if fv.IsNil() {
						continue
					}

					ft = fv.Elem().Type()
				}

				if !holdsStore(ft, 0) {
					continue
				}

				if ft.Implements(snapType) {
					continue
				}

				key := prov.name + "/" + f.Name
				if _, ok := guardExclude[key]; ok {
					continue
				}

				missing++

				t.Errorf("%s: field %s (%s) holds a memstore.Store but is not Snapshottable — "+
					"add persistence (see #582) or justify in guardExclude", prov.name, f.Name, ft)
			}
		})
	}

	if missing == 0 && len(guardExclude) != 0 {
		t.Errorf("guardExclude has %d entr(ies) but nothing is missing — prune stale exclusions", len(guardExclude))
	}
}

// holdsStore reports whether a *memstore.Store is reachable from type t within
// maxStoreScanDepth nested struct/pointer hops. It walks STATIC types, so an
// interface type is opaque to it; the caller resolves an interface-typed
// provider field to its runtime concrete type before calling this, so the mock
// behind it is inspected. This is the same criterion used to compute the set of
// services persistence must cover.
func holdsStore(t reflect.Type, depth int) bool {
	if depth > maxStoreScanDepth || t == nil {
		return false
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if strings.Contains(t.String(), "memstore.Store") {
		return true
	}

	if t.Kind() != reflect.Struct {
		return false
	}

	for i := range t.NumField() {
		ft := t.Field(i).Type
		if strings.Contains(ft.String(), "memstore.Store") {
			return true
		}

		if k := ft.Kind(); k == reflect.Pointer || k == reflect.Struct {
			if holdsStore(ft, depth+1) {
				return true
			}
		}
	}

	return false
}

// handlerStoreDebt lists the server/ packages that still build a memstore in a
// wire handler, so that state is outside every provider snapshot and lost by
// serve --persist on restart. Remove an entry when its state moves into a
// persisted provider; do not add one.
//
//nolint:gochecknoglobals // test fixture: the reviewed ratchet of known gaps.
var handlerStoreDebt = map[string]struct{}{
	"server/aws/ec2":          {},
	"server/aws/savingsplans": {},
	"server/azure/eventhub":   {},
	"server/azure/kusto":      {},
	"server/azure/servicebus": {},
	"server/azure/synapse":    {},
	"server/gcp/firestore":    {},
	"server/oci/workrequest":  {},
}

// TestNoNewHandlerStores is the wire-side half of the completeness guard.
// TestSnapshotCompleteness only sees provider fields, so state a server/
// handler keeps for itself never reaches it. This ratchet fails when a new
// server/ package builds a memstore (the store a stateful handler reaches for
// first). It cannot see plain maps, which many handlers use for caches and
// static catalogs, so it narrows the gap rather than closing it.
func TestNoNewHandlerStores(t *testing.T) {
	root := filepath.Join("..", "server")
	found := map[string]struct{}{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if strings.Contains(string(src), "memstore.New") {
			rel, _ := filepath.Rel("..", filepath.Dir(path))
			found[filepath.ToSlash(rel)] = struct{}{}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk server/: %v", err)
	}

	pkgs := make([]string, 0, len(found))
	for p := range found {
		pkgs = append(pkgs, p)
	}

	sort.Strings(pkgs)

	for _, p := range pkgs {
		if _, ok := handlerStoreDebt[p]; !ok {
			t.Errorf("%s builds a memstore in a wire handler: move the state into a persisted provider "+
				"(Snapshottable) so serve --persist keeps it", p)
		}
	}

	for p := range handlerStoreDebt {
		if _, ok := found[p]; !ok {
			t.Errorf("handlerStoreDebt lists %s but it no longer builds a memstore: remove the entry", p)
		}
	}
}

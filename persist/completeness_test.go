package persist_test

import (
	"reflect"
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
// survive a restart — and then only WITH a comment here justifying why. It is
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
// OCI is intentionally covered-by-criterion-but-empty: the OCI Provider exposes
// its services as DRIVER INTERFACES (netdriver.Networking, iamdriver.IAM, ...),
// not concrete *Mock structs, so holdsStore — which walks static struct/pointer
// types — never reaches the memstore.Store the concrete OCI mocks hold behind
// those interfaces. OCI persistence is therefore out of scope by this guard's
// criterion (the concrete OCI mocks do use memstore.Store; persisting them is a
// separate follow-up). The AWS/Azure/GCP providers expose concrete *Mock fields,
// so they are fully covered.
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

				if !holdsStore(f.Type, 0) {
					continue
				}

				if f.Type.Implements(snapType) {
					continue
				}

				key := prov.name + "/" + f.Name
				if _, ok := guardExclude[key]; ok {
					continue
				}

				missing++

				t.Errorf("%s: field %s (%s) holds a memstore.Store but is not Snapshottable — "+
					"add persistence (see #582) or justify in guardExclude", prov.name, f.Name, f.Type)
			}
		})
	}

	if missing == 0 && len(guardExclude) != 0 {
		t.Errorf("guardExclude has %d entr(ies) but nothing is missing — prune stale exclusions", len(guardExclude))
	}
}

// holdsStore reports whether a *memstore.Store is reachable from type t within
// maxStoreScanDepth nested struct/pointer hops. It walks STATIC types, so a field
// typed as an interface (as every OCI service field is) is opaque and returns
// false — the concrete mock behind it is never inspected. This is the same
// criterion used to compute the set of services persistence must cover.
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

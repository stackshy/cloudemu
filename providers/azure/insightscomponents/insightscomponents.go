// Package insightscomponents holds Azure Application Insights components
// (Microsoft.Insights/components) in a persisted store, so serve --persist keeps
// them, their computed keys and their billing features across a restart. The ARM
// wire handler in server/azure/appinsights owns the request semantics.
package insightscomponents

import (
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// Component is a stored Application Insights component. The writable properties
// are kept in Props as a generic map so a GET/LIST echoes back exactly what the
// caller PUT (plus the injected defaults), while the fields Azure computes ONCE
// at create (InstrumentationKey, AppID, TenantID, CreationDate) are held as
// dedicated fields so they never change on a subsequent PUT/PATCH. Real Azure
// documents that "you cannot specify a different value for InstrumentationKey
// nor AppId in the Put operation", so regenerating them per-GET would be
// perpetual Terraform drift.
type Component struct {
	Subscription  string
	ResourceGroup string
	Name          string
	Location      string
	Kind          string
	Tags          map[string]string

	// Computed once at create and never mutated afterwards.
	InstrumentationKey string
	AppID              string
	TenantID           string
	CreationDate       string

	// Writable properties (Application_Type, Flow_Type, RetentionInDays, ...) as
	// supplied by the caller with defaults filled in.
	Props map[string]any

	// Billing is the currentbillingfeatures child as last PUT, nil until the
	// first write (a read then returns the defaults). It lives and dies with the
	// component.
	Billing map[string]any
}

// Mock is the component store, keyed case-insensitively by the component's full
// (subscription, resourceGroup, name) scope. Component names are unique only
// within a subscription and resource group, so all three segments key the entry.
type Mock struct {
	store *memstore.Store[*Component]
}

// New returns an empty component store.
func New() *Mock {
	return &Mock{store: memstore.New[*Component]()}
}

func key(sub, rg, name string) string {
	return strings.ToLower(sub + "/" + rg + "/" + name)
}

// Get returns the component at sub/rg/name. Callers copy before mutating.
func (m *Mock) Get(sub, rg, name string) (*Component, bool) {
	return m.store.Get(key(sub, rg, name))
}

// Set stores c under its (subscription, resourceGroup, name) scope.
func (m *Mock) Set(c *Component) {
	m.store.Set(key(c.Subscription, c.ResourceGroup, c.Name), c)
}

// Delete removes the component at sub/rg/name, reporting whether it existed.
func (m *Mock) Delete(sub, rg, name string) bool {
	return m.store.Delete(key(sub, rg, name))
}

// ListBy returns every component in a subscription, optionally narrowed to one
// resource group (an empty resourceGroup lists the whole subscription), sorted
// by store key so the list order is deterministic.
func (m *Mock) ListBy(sub, resourceGroup string) []*Component {
	all := m.store.All()

	keys := make([]string, 0, len(all))

	for k, c := range all {
		if !strings.EqualFold(c.Subscription, sub) {
			continue
		}

		if resourceGroup != "" && !strings.EqualFold(c.ResourceGroup, resourceGroup) {
			continue
		}

		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]*Component, 0, len(keys))
	for _, k := range keys {
		out = append(out, all[k])
	}

	return out
}

// Purge deletes every component under sub/rg, backing the resource-group cascade.
func (m *Mock) Purge(sub, rg string) {
	for k, c := range m.store.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}
}

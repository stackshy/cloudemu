package appinsights

import (
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// componentState is a stored Application Insights component
// (Microsoft.Insights/components). The writable properties are kept in Props as
// a generic map so a GET/LIST echoes back exactly what the caller PUT (plus the
// injected defaults), while the fields Azure computes ONCE at create —
// InstrumentationKey, AppID, TenantID, CreationDate — are held as dedicated
// fields so they never change on a subsequent PUT/PATCH. Real Azure documents
// that "you cannot specify a different value for InstrumentationKey nor AppId in
// the Put operation", so regenerating them per-GET would be perpetual Terraform
// drift; storing them once (and deriving them deterministically) keeps them
// stable across every read.
type componentState struct {
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

	// Writable properties (Application_Type, Flow_Type, RetentionInDays, …) as
	// supplied by the caller with defaults filled in. Computed keys are never
	// stored here — they live in the dedicated fields above.
	Props map[string]any
}

// store is the concurrency-safe backing map, keyed case-insensitively by the
// component's full (subscription, resourceGroup, name) scope. Component names are
// unique only within a subscription+resource group, so all three segments key
// the entry — keying by name alone would let a list at one resource group return
// another group's components.
type store struct {
	m *memstore.Store[*componentState]
}

func newStore() *store {
	return &store{m: memstore.New[*componentState]()}
}

// key builds the case-insensitive store key for a component scope.
func key(sub, rg, name string) string {
	return strings.ToLower(sub + "/" + rg + "/" + name)
}

func (s *store) get(sub, rg, name string) (*componentState, bool) {
	return s.m.Get(key(sub, rg, name))
}

// set stores cs under its (subscription, resourceGroup, name) scope. The caller
// determines create-vs-update (200 vs 201) from a prior get, so set has no
// return.
func (s *store) set(cs *componentState) {
	s.m.Set(key(cs.Subscription, cs.ResourceGroup, cs.Name), cs)
}

func (s *store) delete(sub, rg, name string) bool {
	return s.m.Delete(key(sub, rg, name))
}

// listBy returns every component in a subscription, optionally narrowed to one
// resource group (an empty resourceGroup lists the whole subscription), sorted
// by store key so the list order is deterministic.
func (s *store) listBy(sub, resourceGroup string) []*componentState {
	all := s.m.All()

	keys := make([]string, 0, len(all))

	for k, cs := range all {
		if !strings.EqualFold(cs.Subscription, sub) {
			continue
		}

		if resourceGroup != "" && !strings.EqualFold(cs.ResourceGroup, resourceGroup) {
			continue
		}

		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]*componentState, 0, len(keys))
	for _, k := range keys {
		out = append(out, all[k])
	}

	return out
}

// purge deletes every component under sub/rg, backing the resource-group cascade.
func (s *store) purge(sub, rg string) {
	for k, cs := range s.m.All() {
		if strings.EqualFold(cs.Subscription, sub) && strings.EqualFold(cs.ResourceGroup, rg) {
			s.m.Delete(k)
		}
	}
}

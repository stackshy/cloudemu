package monitor

import "sync"

// armResource is a stored microsoft.insights ARM resource (metric alert, action
// group, activity-log alert). The full request body is retained — location,
// tags and the entire properties object — so a GET/LIST echoes back exactly
// what the caller PUT, instead of a hardcoded stub. This is the fix for the
// drift where every stored property was dropped and only provisioningState came
// back.
type armResource struct {
	Location   string
	Tags       map[string]string
	Properties map[string]any
}

// resourceKey identifies a stored resource by its full ARM scope: subscription,
// resource group, resource kind (metricAlerts/actionGroups/activityLogAlerts)
// and name. Real Azure resource names are only unique within one resource
// group, so the subscription and resource group must be part of the key —
// keying by (kind, name) alone let a list() at one resource group return
// another resource group's (or another subscription's) resources.
type resourceKey struct {
	subscription  string
	resourceGroup string
	kind          string
	name          string
}

// resourceStore is a concurrency-safe map of resourceKey to resource.
type resourceStore struct {
	mu sync.RWMutex
	m  map[resourceKey]*armResource
}

func newResourceStore() *resourceStore {
	return &resourceStore{m: make(map[resourceKey]*armResource)}
}

// set stores res under the (subscription, resourceGroup, kind, name) key and
// reports whether an entry already existed (so the caller can answer 200 vs
// 201).
func (s *resourceStore) set(subscription, resourceGroup, kind, name string, res *armResource) (existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := resourceKey{subscription: subscription, resourceGroup: resourceGroup, kind: kind, name: name}
	_, existed = s.m[key]
	s.m[key] = res

	return existed
}

func (s *resourceStore) get(subscription, resourceGroup, kind, name string) (*armResource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res, ok := s.m[resourceKey{subscription: subscription, resourceGroup: resourceGroup, kind: kind, name: name}]

	return res, ok
}

func (s *resourceStore) delete(subscription, resourceGroup, kind, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := resourceKey{subscription: subscription, resourceGroup: resourceGroup, kind: kind, name: name}
	if _, ok := s.m[key]; !ok {
		return false
	}

	delete(s.m, key)

	return true
}

// allOfKind returns every stored resource of one kind across ALL subscriptions
// and resource groups, keyed by its full resourceKey. Referential-integrity
// checks (e.g. is an action group still referenced by any metricAlert or
// activityLogAlert) must consider the whole store, not one scope: a metric
// alert can name an action group id from a different resource group than its
// own, exactly like the actual scope segments in an ARM resource id.
func (s *resourceStore) allOfKind(kind string) map[resourceKey]*armResource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[resourceKey]*armResource)

	for k, v := range s.m {
		if k.kind == kind {
			out[k] = v
		}
	}

	return out
}

// all returns the stored resources of one kind scoped to one subscription,
// keyed by name. When resourceGroup is non-empty the result is further scoped
// to that resource group (a "list by resource group" request); an empty
// resourceGroup returns every resource group's resources in the subscription
// (a "list by subscription" request), matching the two list scopes the real
// metricAlerts/actionGroups/activityLogAlerts APIs support.
func (s *resourceStore) all(subscription, resourceGroup, kind string) map[string]*armResource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]*armResource)

	for k, v := range s.m {
		if k.subscription != subscription || k.kind != kind {
			continue
		}

		if resourceGroup != "" && k.resourceGroup != resourceGroup {
			continue
		}

		out[k.name] = v
	}

	return out
}

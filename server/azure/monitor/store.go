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

// resourceStore is a concurrency-safe map of resource-type to name to resource.
type resourceStore struct {
	mu sync.RWMutex
	m  map[string]map[string]*armResource
}

func newResourceStore() *resourceStore {
	return &resourceStore{m: make(map[string]map[string]*armResource)}
}

// set stores res under the (kind, name) key and reports whether an entry already
// existed (so the caller can answer 200 vs 201).
func (s *resourceStore) set(kind, name string, res *armResource) (existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byName := s.m[kind]
	if byName == nil {
		byName = make(map[string]*armResource)
		s.m[kind] = byName
	}

	_, existed = byName[name]
	byName[name] = res

	return existed
}

func (s *resourceStore) get(kind, name string) (*armResource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res, ok := s.m[kind][name]

	return res, ok
}

func (s *resourceStore) delete(kind, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	byName := s.m[kind]
	if _, ok := byName[name]; !ok {
		return false
	}

	delete(byName, name)

	return true
}

// names returns the stored resource names for a kind, in the map's iteration
// order. Callers that need determinism sort the result.
func (s *resourceStore) all(kind string) map[string]*armResource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	src := s.m[kind]
	out := make(map[string]*armResource, len(src))

	for k, v := range src {
		out[k] = v
	}

	return out
}

package accesscontextmanager

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

// CreatePolicy provisions a new access policy with a deterministic numeric name.
func (m *Mock) CreatePolicy(_ context.Context, cfg *acmdriver.PolicyConfig) (
	*acmdriver.Policy, *acmdriver.Operation, error,
) {
	parent := stringField(cfg.Fields, "parent")
	title := stringField(cfg.Fields, "title")

	if parent == "" || title == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "parent and title are required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	number := policyNumber(parent, title)
	key := policyName(number)

	if m.policies.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "access policy %q already exists", key)
	}

	now := m.opts.Clock.Now().UTC()
	p := acmdriver.Policy{
		Number:     number,
		CreateTime: now,
		UpdateTime: now,
		Etag:       m.nextEtag(key),
		Fields:     cloneRawMap(cfg.Fields),
	}
	m.policies.Set(key, p)

	op := m.newOp("policy", "create", key)
	out := clonePolicy(&p)

	return &out, op, nil
}

// GetPolicy returns an access policy by its numeric name.
func (m *Mock) GetPolicy(_ context.Context, number string) (*acmdriver.Policy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies.Get(policyName(number))
	if !ok {
		return nil, notFound(policyName(number))
	}

	out := clonePolicy(&p)

	return &out, nil
}

// ListPolicies returns every access policy whose parent equals the requested
// organization, ordered by resource name.
func (m *Mock) ListPolicies(_ context.Context, parent string) ([]acmdriver.Policy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.policies.SortedValues()
	out := make([]acmdriver.Policy, 0, len(all))

	for i := range all {
		if parent != "" && stringField(all[i].Fields, "parent") != parent {
			continue
		}

		out = append(out, clonePolicy(&all[i]))
	}

	return out, nil
}

// PatchPolicy applies a masked update to an access policy and re-mints its etag.
func (m *Mock) PatchPolicy(_ context.Context, number string, cfg *acmdriver.PolicyConfig, mask []string) (
	*acmdriver.Policy, *acmdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := policyName(number)

	p, ok := m.policies.Get(key)
	if !ok {
		return nil, nil, notFound(key)
	}

	p.Fields = applyMask(p.Fields, cfg.Fields, mask)
	p.UpdateTime = m.opts.Clock.Now().UTC()
	p.Etag = m.nextEtag(key)
	m.policies.Set(key, p)

	op := m.newOp("policy", "update", key)
	out := clonePolicy(&p)

	return &out, op, nil
}

// DeletePolicy removes an access policy and every access level and service
// perimeter nested under it.
func (m *Mock) DeletePolicy(_ context.Context, number string) (*acmdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := policyName(number)
	if !m.policies.Has(key) {
		return nil, notFound(key)
	}

	m.policies.Delete(key)
	deleteChildrenOf(m.levels, number)
	deleteChildrenOf(m.perimeters, number)

	return m.newOp("policy", "delete", key), nil
}

// createChild provisions a new level or perimeter under an existing policy.
func (m *Mock) createChild(store *memstore.Store[acmdriver.Child], coll, kind string, cfg *acmdriver.ChildConfig) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, coll+" id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.policies.Has(policyName(cfg.PolicyNumber)) {
		return nil, nil, notFound(policyName(cfg.PolicyNumber))
	}

	key := childName(coll, cfg.PolicyNumber, cfg.ID)
	if store.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "%s %q already exists", coll, key)
	}

	now := m.opts.Clock.Now().UTC()
	c := acmdriver.Child{
		PolicyNumber: cfg.PolicyNumber,
		ID:           cfg.ID,
		CreateTime:   now,
		UpdateTime:   now,
		Etag:         m.nextEtag(key),
		Fields:       cloneRawMap(cfg.Fields),
	}
	store.Set(key, c)

	op := m.newOp(kind, "create", key)
	out := cloneChild(&c)

	return &out, op, nil
}

// getChild returns a level or perimeter by identity, cloned.
func (m *Mock) getChild(store *memstore.Store[acmdriver.Child], coll, policyNumber, id string) (
	*acmdriver.Child, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := store.Get(childName(coll, policyNumber, id))
	if !ok {
		return nil, notFound(childName(coll, policyNumber, id))
	}

	out := cloneChild(&c)

	return &out, nil
}

// listChildren returns every level or perimeter under a policy, ordered by name.
func (m *Mock) listChildren(store *memstore.Store[acmdriver.Child], coll, policyNumber string) (
	[]acmdriver.Child, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := childName(coll, policyNumber, "")
	all := store.SortedValues()
	out := make([]acmdriver.Child, 0, len(all))

	for i := range all {
		if strings.HasPrefix(childName(coll, all[i].PolicyNumber, all[i].ID), prefix) {
			out = append(out, cloneChild(&all[i]))
		}
	}

	return out, nil
}

// patchChild applies a masked update to a level or perimeter and re-mints etag.
func (m *Mock) patchChild(store *memstore.Store[acmdriver.Child], coll string, cfg *acmdriver.ChildConfig, mask []string) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	kind := "servicePerimeter"
	if coll == accessLevelsColl {
		kind = "accessLevel"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := childName(coll, cfg.PolicyNumber, cfg.ID)

	c, ok := store.Get(key)
	if !ok {
		return nil, nil, notFound(key)
	}

	c.Fields = applyMask(c.Fields, cfg.Fields, mask)
	c.UpdateTime = m.opts.Clock.Now().UTC()
	c.Etag = m.nextEtag(key)
	store.Set(key, c)

	op := m.newOp(kind, "update", key)
	out := cloneChild(&c)

	return &out, op, nil
}

// delChild removes a level or perimeter and returns the completed LRO.
func (m *Mock) delChild(store *memstore.Store[acmdriver.Child], coll, kind, policyNumber, id string) (
	*acmdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := childName(coll, policyNumber, id)
	if !store.Has(key) {
		return nil, notFound(key)
	}

	store.Delete(key)

	return m.newOp(kind, "delete", key), nil
}

// CreateAccessLevel provisions a new access level.
func (m *Mock) CreateAccessLevel(_ context.Context, cfg *acmdriver.ChildConfig) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	return m.createChild(m.levels, accessLevelsColl, "accessLevel", cfg)
}

// GetAccessLevel returns an access level by identity.
func (m *Mock) GetAccessLevel(_ context.Context, policyNumber, id string) (*acmdriver.Child, error) {
	return m.getChild(m.levels, accessLevelsColl, policyNumber, id)
}

// ListAccessLevels returns every access level under a policy.
func (m *Mock) ListAccessLevels(_ context.Context, policyNumber string) ([]acmdriver.Child, error) {
	return m.listChildren(m.levels, accessLevelsColl, policyNumber)
}

// PatchAccessLevel applies a masked update to an access level.
func (m *Mock) PatchAccessLevel(_ context.Context, cfg *acmdriver.ChildConfig, mask []string) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	return m.patchChild(m.levels, accessLevelsColl, cfg, mask)
}

// DeleteAccessLevel removes an access level.
func (m *Mock) DeleteAccessLevel(_ context.Context, policyNumber, id string) (*acmdriver.Operation, error) {
	return m.delChild(m.levels, accessLevelsColl, "accessLevel", policyNumber, id)
}

// CreateServicePerimeter provisions a new service perimeter.
func (m *Mock) CreateServicePerimeter(_ context.Context, cfg *acmdriver.ChildConfig) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	return m.createChild(m.perimeters, servicePerimetersColl, "servicePerimeter", cfg)
}

// GetServicePerimeter returns a service perimeter by identity.
func (m *Mock) GetServicePerimeter(_ context.Context, policyNumber, id string) (*acmdriver.Child, error) {
	return m.getChild(m.perimeters, servicePerimetersColl, policyNumber, id)
}

// ListServicePerimeters returns every service perimeter under a policy.
func (m *Mock) ListServicePerimeters(_ context.Context, policyNumber string) ([]acmdriver.Child, error) {
	return m.listChildren(m.perimeters, servicePerimetersColl, policyNumber)
}

// PatchServicePerimeter applies a masked update to a service perimeter.
func (m *Mock) PatchServicePerimeter(_ context.Context, cfg *acmdriver.ChildConfig, mask []string) (
	*acmdriver.Child, *acmdriver.Operation, error,
) {
	return m.patchChild(m.perimeters, servicePerimetersColl, cfg, mask)
}

// DeleteServicePerimeter removes a service perimeter.
func (m *Mock) DeleteServicePerimeter(_ context.Context, policyNumber, id string) (*acmdriver.Operation, error) {
	return m.delChild(m.perimeters, servicePerimetersColl, "servicePerimeter", policyNumber, id)
}

// deleteChildrenOf removes every child of a policy from a store (cascade delete).
func deleteChildrenOf(store *memstore.Store[acmdriver.Child], policyNumber string) {
	for _, key := range store.Keys() {
		if v, ok := store.Get(key); ok && v.PolicyNumber == policyNumber {
			store.Delete(key)
		}
	}
}

// stringField decodes a top-level body field as a JSON string, returning "" when
// it is absent or not a string.
func stringField(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}

	return s
}

// applyMask folds the masked top-level body fields from desired into a copy of
// stored. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(stored, desired map[string]json.RawMessage, mask []string) map[string]json.RawMessage {
	out := cloneRawMap(stored)
	if out == nil {
		out = map[string]json.RawMessage{}
	}

	if len(mask) == 0 {
		for k, v := range desired {
			out[k] = append(json.RawMessage(nil), v...)
		}

		return out
	}

	for _, path := range mask {
		field := path
		if i := strings.IndexByte(path, '.'); i >= 0 {
			field = path[:i]
		}

		if v, ok := desired[field]; ok {
			out[field] = append(json.RawMessage(nil), v...)
			continue
		}

		delete(out, field)
	}

	return out
}

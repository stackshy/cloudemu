package identity

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// rootName is the name real OCI gives the tenancy when it is returned as a
// compartment.
const rootName = "root"

// pathSeparator separates compartment names in a nested path, as OCI writes
// them in a policy location ("compartment parent:child").
const pathSeparator = ":"

// compartment is a node of the compartment tree. Its scope names its parent,
// which is the tenancy for a top-level compartment.
type compartment struct {
	ID           string
	Name         string
	Description  string
	TimeCreated  string
	Scope        scope.Scope
	FreeformTags map[string]string
}

// CreateCompartment creates a compartment under an existing parent.
func (m *Mock) CreateCompartment(_ context.Context, spec driver.CompartmentSpec) (*driver.CompartmentInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if spec.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartment name is required")
	}

	parentID := m.compartmentOr(spec.ParentID)
	if !m.compartmentExists(parentID) {
		return nil, cerrors.Newf(cerrors.NotFound, "parent compartment %q not found", parentID)
	}

	if _, found := m.childNamed(parentID, spec.Name); found {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"compartment %q already exists in %q", spec.Name, parentID)
	}

	c := &compartment{
		ID:           idgen.GlobalOCID(kindCompartment, m.opts.Realm),
		Name:         spec.Name,
		Description:  spec.Description,
		TimeCreated:  m.now(),
		Scope:        scope.Scope{Compartment: parentID},
		FreeformTags: copyTags(spec.FreeformTags),
	}
	m.compartments.Set(c.ID, c)

	return toCompartmentInfo(c), nil
}

// GetCompartment returns a compartment by OCID. The tenancy is the root
// compartment and is returned even though nothing created it.
func (m *Mock) GetCompartment(_ context.Context, id string) (*driver.CompartmentInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if id == m.opts.TenancyOCID {
		return m.rootInfo(), nil
	}

	c, ok := m.compartments.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "compartment %q not found", id)
	}

	return toCompartmentInfo(c), nil
}

// ListCompartments returns the direct children of parentID, or every
// descendant when inSubtree is set.
func (m *Mock) ListCompartments(_ context.Context, parentID string, inSubtree bool) ([]driver.CompartmentInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.compartmentExists(parentID) {
		return nil, cerrors.Newf(cerrors.NotFound, "compartment %q not found", parentID)
	}

	all := m.compartments.SortedValues()
	out := make([]driver.CompartmentInfo, 0, len(all))

	for _, c := range all {
		// covers() is true for the parent itself, which a list never returns.
		if c.Scope.Matches(scope.Scope{Compartment: parentID}) ||
			(inSubtree && c.ID != parentID && m.covers(parentID, c.ID)) {
			out = append(out, *toCompartmentInfo(c))
		}
	}

	return out, nil
}

// UpdateCompartment applies the mutable fields of a compartment.
func (m *Mock) UpdateCompartment(
	_ context.Context, id string, upd driver.IdentityUpdate,
) (*driver.CompartmentInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.compartments.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "compartment %q not found", id)
	}

	if upd.Name != "" && upd.Name != c.Name {
		if _, found := m.childNamed(c.Scope.Compartment, upd.Name); found {
			return nil, cerrors.Newf(cerrors.AlreadyExists,
				"compartment %q already exists in %q", upd.Name, c.Scope.Compartment)
		}

		c.Name = upd.Name
	}

	if upd.Description != "" {
		c.Description = upd.Description
	}

	if upd.FreeformTags != nil {
		c.FreeformTags = copyTags(upd.FreeformTags)
	}

	m.compartments.Set(id, c)

	return toCompartmentInfo(c), nil
}

// DeleteCompartment deletes an empty compartment. Real OCI refuses while
// anything still lives in it.
func (m *Mock) DeleteCompartment(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.compartments.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "compartment %q not found", id)
	}

	if occupant := m.occupantOf(id); occupant != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"compartment %q still contains %s", id, occupant)
	}

	m.compartments.Delete(id)

	return nil
}

// occupantOf names the first resource still living in a compartment, or "" if
// it is empty.
func (m *Mock) occupantOf(id string) string {
	filter := scope.Scope{Compartment: id}

	for _, c := range m.compartments.SortedValues() {
		if c.Scope.Matches(filter) {
			return "compartment " + c.Name
		}
	}

	for _, p := range m.policies.SortedValues() {
		if p.Scope.Matches(filter) {
			return "policy " + p.Name
		}
	}

	return m.principalOccupant(filter)
}

// principalOccupant names the first user or group still in the compartment.
func (m *Mock) principalOccupant(filter scope.Scope) string {
	for _, p := range m.users.SortedValues() {
		if p.Scope.Matches(filter) {
			return "user " + p.Name
		}
	}

	for _, p := range m.groups.SortedValues() {
		if p.Scope.Matches(filter) {
			return "group " + p.Name
		}
	}

	return ""
}

// compartmentExists reports whether id names a compartment or the tenancy.
func (m *Mock) compartmentExists(id string) bool {
	return id == m.opts.TenancyOCID || m.compartments.Has(id)
}

// childNamed returns the child compartment of parentID with the given name.
func (m *Mock) childNamed(parentID, name string) (*compartment, bool) {
	for _, c := range m.compartments.SortedValues() {
		if c.Scope.Compartment == parentID && strings.EqualFold(c.Name, name) {
			return c, true
		}
	}

	return nil, false
}

// covers reports whether target is ancestor or one of its descendants. This is
// where compartmentIdInSubtree and policy locations resolve ancestry; the
// scope package matches exactly on purpose and never walks the tree.
func (m *Mock) covers(ancestor, target string) bool {
	if ancestor == "" || target == "" {
		return false
	}

	// A chain of distinct compartments cannot be longer than the store, so a
	// longer walk means a cycle. Nothing reparents a compartment today; the
	// bound is what keeps a future move operation from looping forever.
	for id, steps := target, m.compartments.Len(); id != "" && steps >= 0; steps-- {
		if id == ancestor {
			return true
		}

		c, ok := m.compartments.Get(id)
		if !ok {
			return false
		}

		id = c.Scope.Compartment
	}

	return false
}

// resolvePath walks a colon-separated compartment path down from base.
func (m *Mock) resolvePath(base, path string) (string, bool) {
	current := base

	for _, name := range strings.Split(path, pathSeparator) {
		name = strings.TrimSpace(name)
		if name == "" {
			return "", false
		}

		c, ok := m.childNamed(current, name)
		if !ok {
			return "", false
		}

		current = c.ID
	}

	return current, true
}

// rootInfo describes the tenancy as OCI's root compartment.
func (m *Mock) rootInfo() *driver.CompartmentInfo {
	return &driver.CompartmentInfo{
		ID:             m.opts.TenancyOCID,
		Name:           rootName,
		Description:    "root compartment",
		LifecycleState: lifecycleActive,
		FreeformTags:   map[string]string{},
	}
}

func toCompartmentInfo(c *compartment) *driver.CompartmentInfo {
	return &driver.CompartmentInfo{
		ID:             c.ID,
		ParentID:       c.Scope.Compartment,
		Name:           c.Name,
		Description:    c.Description,
		TimeCreated:    c.TimeCreated,
		LifecycleState: lifecycleActive,
		FreeformTags:   copyTags(c.FreeformTags),
	}
}

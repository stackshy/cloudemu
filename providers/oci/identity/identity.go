// Package identity provides an in-memory mock implementation of OCI Identity.
package identity

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time checks that Mock implements the portable driver and the three
// OCI-shaped capabilities the wire handler discovers by type assertion.
var (
	_ driver.IAM               = (*Mock)(nil)
	_ driver.Compartments      = (*Mock)(nil)
	_ driver.OCIIdentity       = (*Mock)(nil)
	_ driver.StatementPolicies = (*Mock)(nil)
)

const (
	timeFormat      = time.RFC3339
	lifecycleActive = "ACTIVE"
	defaultPath     = "/"
	// maxNameLength is the limit OCI puts on an identity resource name.
	maxNameLength = 100
)

// OCI resource type segments, as they appear inside an OCID.
const (
	kindUser         = "user"
	kindGroup        = "group"
	kindMembership   = "usergroupmembership"
	kindPolicy       = "policy"
	kindCompartment  = "compartment"
	kindDynamicGroup = "dynamicgroup"
	kindCredential   = "credential"
)

// principal is an OCI user or group; the two carry identical attributes.
type principal struct {
	ID           string
	Name         string
	Description  string
	TimeCreated  string
	Scope        scope.Scope
	FreeformTags map[string]string
}

// membership binds a user to a group and is addressable in its own right.
type membership struct {
	ID          string
	UserID      string
	GroupID     string
	TimeCreated string
	Scope       scope.Scope
}

// dynamicGroup backs the portable driver's roles: it is the OCI resource that
// grants an identity to something other than a user, via a matching rule.
type dynamicGroup struct {
	ID           string
	Name         string
	Description  string
	MatchingRule string
	TimeCreated  string
	Scope        scope.Scope
	FreeformTags map[string]string
}

// authToken backs the portable driver's access keys.
type authToken struct {
	ID          string
	UserName    string
	Token       string
	TimeCreated string
}

// Mock is an in-memory mock implementation of the OCI Identity service.
type Mock struct {
	// mu guards the fields of stored values. Each store locks its own map, but
	// update paths mutate the pointers that map hands back, which readers such
	// as Evaluate walk at the same time.
	mu sync.RWMutex

	users         *memstore.Store[*principal]
	groups        *memstore.Store[*principal]
	memberships   *memstore.Store[*membership]
	policies      *memstore.Store[*policy]
	compartments  *memstore.Store[*compartment]
	dynamicGroups *memstore.Store[*dynamicGroup]
	authTokens    *memstore.Store[*authToken]

	opts *config.Options
}

// New creates a new OCI Identity mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		users:         memstore.New[*principal](),
		groups:        memstore.New[*principal](),
		memberships:   memstore.New[*membership](),
		policies:      memstore.New[*policy](),
		compartments:  memstore.New[*compartment](),
		dynamicGroups: memstore.New[*dynamicGroup](),
		authTokens:    memstore.New[*authToken](),
		opts:          opts,
	}
}

// now returns the current time in the format OCI stamps on identity resources.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// compartmentOr falls back to the configured default compartment.
func (m *Mock) compartmentOr(id string) string {
	if id == "" {
		return m.opts.CompartmentID
	}

	return id
}

// validateName enforces OCI's identity naming rule: up to maxNameLength
// characters of letters, digits, hyphens, underscores and periods.
func validateName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if len(name) > maxNameLength {
		return cerrors.Newf(cerrors.InvalidArgument,
			"%s name %q is longer than %d characters", kind, name, maxNameLength)
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return cerrors.Newf(cerrors.InvalidArgument,
				"%s name %q may contain only letters, digits, hyphens, underscores and periods", kind, name)
		}
	}

	return nil
}

// createPrincipal records a user or group. Names are unique per store, as they
// are per tenancy in real OCI.
func (m *Mock) createPrincipal(
	store *memstore.Store[*principal], kind string, spec driver.PrincipalSpec,
) (*driver.PrincipalInfo, error) {
	if err := validateName(kind, spec.Name); err != nil {
		return nil, err
	}

	if _, found := findByName(store, spec.Name); found {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "%s %q already exists", kind, spec.Name)
	}

	p := &principal{
		ID:           idgen.GlobalOCID(kind, m.opts.Realm),
		Name:         spec.Name,
		Description:  spec.Description,
		TimeCreated:  m.now(),
		Scope:        scope.Scope{Compartment: m.compartmentOr(spec.CompartmentID)},
		FreeformTags: copyTags(spec.FreeformTags),
	}
	store.Set(p.ID, p)

	return toPrincipalInfo(p), nil
}

// getPrincipal looks a user or group up by OCID.
func getPrincipal(store *memstore.Store[*principal], kind, id string) (*driver.PrincipalInfo, error) {
	p, ok := store.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, id)
	}

	return toPrincipalInfo(p), nil
}

// listPrincipals returns the users or groups in one compartment.
func listPrincipals(store *memstore.Store[*principal], compartmentID string) []driver.PrincipalInfo {
	filter := scope.Scope{Compartment: compartmentID}
	all := store.SortedValues()
	out := make([]driver.PrincipalInfo, 0, len(all))

	for _, p := range all {
		if p.Scope.Matches(filter) {
			out = append(out, *toPrincipalInfo(p))
		}
	}

	return out
}

// updatePrincipal applies the mutable fields of a user or group.
func updatePrincipal(
	store *memstore.Store[*principal], kind, id string, upd driver.IdentityUpdate,
) (*driver.PrincipalInfo, error) {
	p, ok := store.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, id)
	}

	if upd.Description != "" {
		p.Description = upd.Description
	}

	if upd.FreeformTags != nil {
		p.FreeformTags = copyTags(upd.FreeformTags)
	}

	store.Set(id, p)

	return toPrincipalInfo(p), nil
}

// findByName returns the first principal with the given name. OCI user and
// group names are unique per tenancy regardless of case, and policy statements
// match them the same way.
func findByName(store *memstore.Store[*principal], name string) (*principal, bool) {
	for _, p := range store.SortedValues() {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}

	return nil, false
}

func toPrincipalInfo(p *principal) *driver.PrincipalInfo {
	return &driver.PrincipalInfo{
		ID:             p.ID,
		CompartmentID:  p.Scope.Compartment,
		Name:           p.Name,
		Description:    p.Description,
		TimeCreated:    p.TimeCreated,
		LifecycleState: lifecycleActive,
		FreeformTags:   copyTags(p.FreeformTags),
	}
}

func toMembershipInfo(mem *membership) *driver.MembershipInfo {
	return &driver.MembershipInfo{
		ID:             mem.ID,
		CompartmentID:  mem.Scope.Compartment,
		UserID:         mem.UserID,
		GroupID:        mem.GroupID,
		TimeCreated:    mem.TimeCreated,
		LifecycleState: lifecycleActive,
	}
}

// copyTags creates a shallow copy of a tags map.
func copyTags(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}

// CreateOCIUser creates a compartment-scoped user.
func (m *Mock) CreateOCIUser(_ context.Context, spec driver.PrincipalSpec) (*driver.PrincipalInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createPrincipal(m.users, kindUser, spec)
}

// GetOCIUser returns the user with the given OCID.
func (m *Mock) GetOCIUser(_ context.Context, id string) (*driver.PrincipalInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return getPrincipal(m.users, kindUser, id)
}

// ListOCIUsers returns the users in one compartment.
func (m *Mock) ListOCIUsers(_ context.Context, compartmentID string) ([]driver.PrincipalInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listPrincipals(m.users, compartmentID), nil
}

// UpdateOCIUser applies the mutable fields of a user.
func (m *Mock) UpdateOCIUser(
	_ context.Context, id string, upd driver.IdentityUpdate,
) (*driver.PrincipalInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return updatePrincipal(m.users, kindUser, id, upd)
}

// DeleteOCIUser deletes a user and its group memberships.
func (m *Mock) DeleteOCIUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deleteUser(id)
}

// deleteUser deletes a user and its group memberships. Callers hold m.mu.
func (m *Mock) deleteUser(id string) error {
	if !m.users.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "user %q not found", id)
	}

	m.dropMemberships(func(mem *membership) bool { return mem.UserID == id })

	return nil
}

// CreateOCIGroup creates a compartment-scoped group.
func (m *Mock) CreateOCIGroup(_ context.Context, spec driver.PrincipalSpec) (*driver.PrincipalInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createPrincipal(m.groups, kindGroup, spec)
}

// GetOCIGroup returns the group with the given OCID.
func (m *Mock) GetOCIGroup(_ context.Context, id string) (*driver.PrincipalInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return getPrincipal(m.groups, kindGroup, id)
}

// ListOCIGroups returns the groups in one compartment.
func (m *Mock) ListOCIGroups(_ context.Context, compartmentID string) ([]driver.PrincipalInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listPrincipals(m.groups, compartmentID), nil
}

// UpdateOCIGroup applies the mutable fields of a group.
func (m *Mock) UpdateOCIGroup(
	_ context.Context, id string, upd driver.IdentityUpdate,
) (*driver.PrincipalInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return updatePrincipal(m.groups, kindGroup, id, upd)
}

// DeleteOCIGroup deletes a group and its memberships.
func (m *Mock) DeleteOCIGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deleteGroup(id)
}

// deleteGroup deletes a group and its memberships. Callers hold m.mu.
func (m *Mock) deleteGroup(id string) error {
	if !m.groups.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "group %q not found", id)
	}

	m.dropMemberships(func(mem *membership) bool { return mem.GroupID == id })

	return nil
}

// CreateOCIGroupMembership adds a user to a group.
func (m *Mock) CreateOCIGroupMembership(_ context.Context, userID, groupID string) (*driver.MembershipInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createMembership(userID, groupID)
}

// createMembership binds a user to a group. Callers hold m.mu.
func (m *Mock) createMembership(userID, groupID string) (*driver.MembershipInfo, error) {
	if !m.users.Has(userID) {
		return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", userID)
	}

	g, ok := m.groups.Get(groupID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "group %q not found", groupID)
	}

	if mem, found := m.findMembership(userID, groupID); found {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "user %q is already a member of group %q: %s",
			userID, groupID, mem.ID)
	}

	mem := &membership{
		ID:          idgen.GlobalOCID(kindMembership, m.opts.Realm),
		UserID:      userID,
		GroupID:     groupID,
		TimeCreated: m.now(),
		Scope:       g.Scope,
	}
	m.memberships.Set(mem.ID, mem)

	return toMembershipInfo(mem), nil
}

// GetOCIGroupMembership returns the membership with the given OCID.
func (m *Mock) GetOCIGroupMembership(_ context.Context, id string) (*driver.MembershipInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mem, ok := m.memberships.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "user group membership %q not found", id)
	}

	return toMembershipInfo(mem), nil
}

// ListOCIGroupMemberships returns the memberships in one compartment, narrowed
// by user or group when either is given.
func (m *Mock) ListOCIGroupMemberships(
	_ context.Context, compartmentID, userID, groupID string,
) ([]driver.MembershipInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	all := m.memberships.SortedValues()
	out := make([]driver.MembershipInfo, 0, len(all))

	for _, mem := range all {
		switch {
		case !mem.Scope.Matches(filter):
		case userID != "" && mem.UserID != userID:
		case groupID != "" && mem.GroupID != groupID:
		default:
			out = append(out, *toMembershipInfo(mem))
		}
	}

	return out, nil
}

// DeleteOCIGroupMembership removes a user from a group.
func (m *Mock) DeleteOCIGroupMembership(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deleteMembership(id)
}

// deleteMembership removes a user from a group. Callers hold m.mu.
func (m *Mock) deleteMembership(id string) error {
	if !m.memberships.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "user group membership %q not found", id)
	}

	return nil
}

// findMembership returns the membership binding a user to a group.
func (m *Mock) findMembership(userID, groupID string) (*membership, bool) {
	for _, mem := range m.memberships.SortedValues() {
		if mem.UserID == userID && mem.GroupID == groupID {
			return mem, true
		}
	}

	return nil, false
}

// dropMemberships deletes every membership matching the predicate.
func (m *Mock) dropMemberships(match func(*membership) bool) {
	for _, mem := range m.memberships.SortedValues() {
		if match(mem) {
			m.memberships.Delete(mem.ID)
		}
	}
}

// groupNamesFor returns the names of the groups a user belongs to.
func (m *Mock) groupNamesFor(userID string) []string {
	var names []string

	for _, mem := range m.memberships.SortedValues() {
		if mem.UserID != userID {
			continue
		}

		if g, ok := m.groups.Get(mem.GroupID); ok {
			names = append(names, g.Name, g.ID)
		}
	}

	return names
}

package cognito

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// maxGroupNameLen is the GroupNameType length ceiling.
const maxGroupNameLen = 128

func groupKey(poolID, name string) string { return poolID + clientKeySep + name }

func groupNotFound() error { return resourceNotFound("Group not found.") }

//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyGroup(in driver.Group) driver.Group {
	out := in
	out.Precedence = copyInt32Ptr(in.Precedence)

	return out
}

// checkLimit rejects a list Limit above the cognito-idp ceiling of 60.
func checkLimit(n int32) error {
	if n > defaultPageSize {
		return invalidParameter("1 validation error detected: Value '%d' at 'limit' failed to satisfy constraint: "+
			"Member must have value less than or equal to %d", n, defaultPageSize)
	}

	return nil
}

func checkGroupInput(name string, precedence *int32) error {
	if name == "" || len(name) > maxGroupNameLen {
		return invalidParameter("1 validation error detected: Value at 'groupName' failed to satisfy constraint: " +
			"Member must have length between 1 and 128")
	}

	if precedence != nil && *precedence < 0 {
		return invalidParameter("1 validation error detected: Value '%d' at 'precedence' failed to satisfy constraint: "+
			"Member must have value greater than or equal to 0", *precedence)
	}

	return nil
}

// CreateGroup creates a group in a user pool.
func (m *Mock) CreateGroup(_ context.Context, in driver.CreateGroupInput) (*driver.Group, error) {
	if err := checkGroupInput(in.GroupName, in.Precedence); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.userPools.Has(in.UserPoolID) {
		return nil, poolNotFound(in.UserPoolID)
	}

	key := groupKey(in.UserPoolID, in.GroupName)
	if m.groups.Has(key) {
		return nil, &driver.APIError{
			Exception: driver.ExGroupExists,
			Err:       errors.New(errors.AlreadyExists, "A group with the name "+in.GroupName+" already exists."),
		}
	}

	now := m.now()
	g := driver.Group{
		GroupName:        in.GroupName,
		UserPoolID:       in.UserPoolID,
		Description:      in.Description,
		RoleARN:          in.RoleARN,
		Precedence:       copyInt32Ptr(in.Precedence),
		CreationDate:     now,
		LastModifiedDate: now,
	}
	m.groups.Set(key, copyGroup(g))

	return &g, nil
}

// GetGroup returns one group.
func (m *Mock) GetGroup(_ context.Context, userPoolID, groupName string) (*driver.Group, error) {
	if !m.userPools.Has(userPoolID) {
		return nil, poolNotFound(userPoolID)
	}

	g, ok := m.groups.Get(groupKey(userPoolID, groupName))
	if !ok {
		return nil, groupNotFound()
	}

	out := copyGroup(g)

	return &out, nil
}

// UpdateGroup changes the description, role and precedence the input sets.
func (m *Mock) UpdateGroup(_ context.Context, in driver.UpdateGroupInput) (*driver.Group, error) {
	if err := checkGroupInput(in.GroupName, in.Precedence); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.userPools.Has(in.UserPoolID) {
		return nil, poolNotFound(in.UserPoolID)
	}

	key := groupKey(in.UserPoolID, in.GroupName)

	g, ok := m.groups.Get(key)
	if !ok {
		return nil, groupNotFound()
	}

	g = copyGroup(g)

	if in.Description != nil {
		g.Description = *in.Description
	}

	if in.RoleARN != nil {
		g.RoleARN = *in.RoleARN
	}

	if in.Precedence != nil {
		g.Precedence = copyInt32Ptr(in.Precedence)
	}

	g.LastModifiedDate = m.now()
	m.groups.Set(key, copyGroup(g))

	return &g, nil
}

// DeleteGroup removes a group and its memberships.
func (m *Mock) DeleteGroup(_ context.Context, userPoolID, groupName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.userPools.Has(userPoolID) {
		return poolNotFound(userPoolID)
	}

	if !m.groups.Delete(groupKey(userPoolID, groupName)) {
		return groupNotFound()
	}

	for _, key := range m.poolUserKeys(userPoolID) {
		rec, ok := m.users.Get(key)
		if !ok || !slices.Contains(rec.Groups, groupName) {
			continue
		}

		rec = copyUserRecord(rec)
		rec.Groups = slices.DeleteFunc(rec.Groups, func(g string) bool { return g == groupName })
		m.users.Set(key, rec)
	}

	return nil
}

// ListGroups returns a page of a pool's groups sorted by name.
func (m *Mock) ListGroups(_ context.Context, userPoolID string, page driver.Pagination) ([]driver.Group, string, error) {
	if err := checkLimit(page.MaxResults); err != nil {
		return nil, "", err
	}

	if !m.userPools.Has(userPoolID) {
		return nil, "", poolNotFound(userPoolID)
	}

	return paginate(m.poolGroups(userPoolID), page)
}

// AdminAddUserToGroup adds a user to a group. Adding a member again is a no-op.
func (m *Mock) AdminAddUserToGroup(_ context.Context, userPoolID, username, groupName string) error {
	return m.changeMembership(userPoolID, username, groupName, func(groups []string) []string {
		if slices.Contains(groups, groupName) {
			return groups
		}

		return append(groups, groupName)
	})
}

// AdminRemoveUserFromGroup removes a user from a group.
func (m *Mock) AdminRemoveUserFromGroup(_ context.Context, userPoolID, username, groupName string) error {
	return m.changeMembership(userPoolID, username, groupName, func(groups []string) []string {
		return slices.DeleteFunc(groups, func(g string) bool { return g == groupName })
	})
}

func (m *Mock) changeMembership(userPoolID, username, groupName string, fn func([]string) []string) error {
	return m.updateUser(userPoolID, username, func(pool *driver.UserPool, _ string, rec *userRecord) error {
		if !m.groups.Has(groupKey(pool.ID, groupName)) {
			return groupNotFound()
		}

		rec.Groups = fn(rec.Groups)

		return nil
	})
}

// AdminListGroupsForUser returns a page of the groups a user belongs to.
func (m *Mock) AdminListGroupsForUser(
	_ context.Context, userPoolID, username string, page driver.Pagination,
) ([]driver.Group, string, error) {
	if err := checkLimit(page.MaxResults); err != nil {
		return nil, "", err
	}

	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return nil, "", poolNotFound(userPoolID)
	}

	_, rec, ok := m.resolveUser(&pool, username)
	if !ok {
		return nil, "", userNotFound()
	}

	return paginate(m.userGroups(userPoolID, rec.Groups), page)
}

// ListUsersInGroup returns a page of a group's members sorted by username.
func (m *Mock) ListUsersInGroup(_ context.Context, userPoolID, groupName string, page driver.Pagination) ([]driver.User, string, error) {
	if err := checkLimit(page.MaxResults); err != nil {
		return nil, "", err
	}

	if !m.userPools.Has(userPoolID) {
		return nil, "", poolNotFound(userPoolID)
	}

	if !m.groups.Has(groupKey(userPoolID, groupName)) {
		return nil, "", groupNotFound()
	}

	var members []driver.User

	users := m.poolUsers(userPoolID)
	for i := range users {
		if slices.Contains(users[i].Groups, groupName) {
			members = append(members, copyUserRecord(users[i]).User)
		}
	}

	return paginate(members, page)
}

// poolGroups returns a pool's groups sorted by name.
func (m *Mock) poolGroups(poolID string) []driver.Group {
	prefix := groupKey(poolID, "")

	keys := slices.DeleteFunc(m.groups.Keys(), func(k string) bool { return !strings.HasPrefix(k, prefix) })
	sort.Strings(keys)

	out := make([]driver.Group, 0, len(keys))

	for _, k := range keys {
		if g, ok := m.groups.Get(k); ok {
			out = append(out, copyGroup(g))
		}
	}

	return out
}

// userGroups resolves a user's group names to groups sorted by name, skipping
// any that no longer exist.
func (m *Mock) userGroups(poolID string, names []string) []driver.Group {
	out := make([]driver.Group, 0, len(names))

	for _, name := range names {
		if g, ok := m.groups.Get(groupKey(poolID, name)); ok {
			out = append(out, copyGroup(g))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].GroupName < out[j].GroupName })

	return out
}

// deletePoolGroups removes every group of a pool.
func (m *Mock) deletePoolGroups(poolID string) {
	prefix := groupKey(poolID, "")

	for _, k := range m.groups.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.groups.Delete(k)
		}
	}
}

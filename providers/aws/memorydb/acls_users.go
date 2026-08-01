package memorydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func cloneACL(in *mdbdriver.ACL) mdbdriver.ACL {
	a := *in
	a.UserNames = cloneStrings(a.UserNames)
	a.Clusters = cloneStrings(a.Clusters)
	a.Tags = copyTags(a.Tags)

	return a
}

func cloneUser(in *mdbdriver.User) mdbdriver.User {
	u := *in
	u.ACLNames = cloneStrings(u.ACLNames)
	u.Tags = copyTags(u.Tags)

	return u
}

// linkACLCluster adds/removes a cluster name on an ACL's Clusters list. The
// caller holds the write lock.
func (m *Mock) linkACLCluster(aclName, clusterName string, add bool) {
	acl, ok := m.acls.Get(aclName)
	if !ok {
		return
	}

	acl.Clusters = removeStr(acl.Clusters, clusterName)
	if add {
		acl.Clusters = append(acl.Clusters, clusterName)
	}

	m.acls.Set(aclName, acl)
}

func removeStr(items []string, s string) []string {
	out := items[:0:0]

	for _, v := range items {
		if v != s {
			out = append(out, v)
		}
	}

	return out
}

// dedupeStrings returns items with duplicates removed, preserving first-seen
// order.
func dedupeStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))

	for _, v := range items {
		if _, ok := seen[v]; ok {
			continue
		}

		seen[v] = struct{}{}

		out = append(out, v)
	}

	return out
}

func containsStr(items []string, s string) bool {
	for _, v := range items {
		if v == s {
			return true
		}
	}

	return false
}

// ---- ACLs ----

// CreateACL creates an ACL over existing users.
func (m *Mock) CreateACL(_ context.Context, name string, userNames []string, tags map[string]string) (*mdbdriver.ACL, error) {
	if err := validName("ACL", name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.acls.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "ACL %q already exists", name)
	}

	for _, u := range userNames {
		if !m.users.Has(u) {
			return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", u)
		}
	}

	users := dedupeStrings(userNames)

	acl := mdbdriver.ACL{
		Name: name, ARN: m.arn("acl", name), Status: mdbdriver.StatusAvailable,
		MinimumEngineVersion: "6.2", UserNames: users, Tags: copyTags(tags),
	}
	m.acls.Set(name, acl)
	m.attachUsersToACL(name, users)

	out := cloneACL(&acl)

	return &out, nil
}

func (m *Mock) attachUsersToACL(aclName string, userNames []string) {
	for _, un := range userNames {
		if u, ok := m.users.Get(un); ok && !containsStr(u.ACLNames, aclName) {
			u.ACLNames = append(u.ACLNames, aclName)
			m.users.Set(un, u)
		}
	}
}

// DescribeACLs returns all ACLs, or the named ones.
func (m *Mock) DescribeACLs(_ context.Context, names []string) ([]mdbdriver.ACL, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.acls, names, cloneACL, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "ACL %q not found", n)
	})
}

// UpdateACL adds and/or removes user names on an ACL.
func (m *Mock) UpdateACL(_ context.Context, name string, add, remove []string) (*mdbdriver.ACL, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	acl, ok := m.acls.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "ACL %q not found", name)
	}

	for _, u := range add {
		if !m.users.Has(u) {
			return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", u)
		}
	}

	for _, u := range add {
		if !containsStr(acl.UserNames, u) {
			acl.UserNames = append(acl.UserNames, u)
		}
	}

	for _, u := range remove {
		acl.UserNames = removeStr(acl.UserNames, u)
		m.detachUserFromACL(u, name)
	}

	m.acls.Set(name, acl)
	m.attachUsersToACL(name, add)

	out := cloneACL(&acl)

	return &out, nil
}

func (m *Mock) detachUserFromACL(userName, aclName string) {
	if u, ok := m.users.Get(userName); ok {
		u.ACLNames = removeStr(u.ACLNames, aclName)
		m.users.Set(userName, u)
	}
}

// DeleteACL removes an ACL; it must not be attached to any cluster.
func (m *Mock) DeleteACL(_ context.Context, name string) (*mdbdriver.ACL, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	acl, ok := m.acls.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "ACL %q not found", name)
	}

	if len(acl.Clusters) > 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "ACL %q is attached to clusters %v", name, acl.Clusters)
	}

	for _, un := range acl.UserNames {
		m.detachUserFromACL(un, name)
	}

	m.acls.Delete(name)

	out := cloneACL(&acl)

	return &out, nil
}

// ---- Users ----

const authTypePassword = "password"

func authType(t string) string {
	if t == "" {
		return authTypePassword
	}

	return t
}

// CreateUser creates a MemoryDB user.
//
//nolint:gocritic // cfg is large but matches the driver signature.
func (m *Mock) CreateUser(_ context.Context, cfg mdbdriver.CreateUserConfig) (*mdbdriver.User, error) {
	if err := validName("user", cfg.Name); err != nil {
		return nil, err
	}

	if cfg.AccessString == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "accessString is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.users.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "user %q already exists", cfg.Name)
	}

	user := mdbdriver.User{
		Name: cfg.Name, ARN: m.arn("user", cfg.Name), Status: mdbdriver.StatusAvailable,
		AccessString: cfg.AccessString, MinimumEngineVersion: "6.2",
		Authentication: mdbdriver.Authentication{Type: authType(cfg.AuthenticationType), PasswordCount: len(cfg.Passwords)},
		Tags:           copyTags(cfg.Tags),
	}
	m.users.Set(cfg.Name, user)

	out := cloneUser(&user)

	return &out, nil
}

// DescribeUsers returns all users, or the named ones.
func (m *Mock) DescribeUsers(_ context.Context, names []string) ([]mdbdriver.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.users, names, cloneUser, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "user %q not found", n)
	})
}

// UpdateUser updates a user's access string and/or authentication.
func (m *Mock) UpdateUser(_ context.Context, cfg mdbdriver.UpdateUserConfig) (*mdbdriver.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", cfg.Name)
	}

	if cfg.AccessString != "" {
		u.AccessString = cfg.AccessString
	}

	if cfg.AuthenticationType != "" {
		u.Authentication = mdbdriver.Authentication{Type: authType(cfg.AuthenticationType), PasswordCount: len(cfg.Passwords)}
	}

	m.users.Set(cfg.Name, u)

	out := cloneUser(&u)

	return &out, nil
}

// DeleteUser removes a user; it must not be a member of any ACL.
func (m *Mock) DeleteUser(_ context.Context, name string) (*mdbdriver.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", name)
	}

	if len(u.ACLNames) > 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "user %q belongs to ACLs %v", name, u.ACLNames)
	}

	m.users.Delete(name)

	out := cloneUser(&u)

	return &out, nil
}

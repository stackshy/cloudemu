package transfer

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// CreateUser creates a user on a server and increments the server's UserCount.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateUser(_ context.Context, in driver.User) error {
	if in.UserName == "" {
		return invalidRequest("UserName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.servers.Get(in.ServerID); !ok {
		return notFound("server %s does not exist", in.ServerID)
	}

	key := userKey(in.ServerID, in.UserName)
	if _, ok := m.users.Get(key); ok {
		return alreadyExists("user %s already exists on server %s", in.UserName, in.ServerID)
	}

	if in.HomeDirectoryType == "" {
		in.HomeDirectoryType = driver.HomeDirectoryPath
	}

	in.Arn = m.userARN(in.ServerID, in.UserName)

	m.users.Set(key, copyUser(in))
	m.adjustUserCount(in.ServerID, 1)

	if len(in.Tags) > 0 {
		m.storeTags(in.Arn, in.Tags)
	}

	return nil
}

// DescribeUser returns a deep copy of a user (including its imported SSH keys).
func (m *Mock) DescribeUser(_ context.Context, serverID, userName string) (*driver.User, error) {
	u, ok := m.users.Get(userKey(serverID, userName))
	if !ok {
		return nil, notFound("user %s does not exist on server %s", userName, serverID)
	}

	out := copyUser(u)

	return &out, nil
}

// UpdateUser applies the mutable-field delta to a user.
func (m *Mock) UpdateUser(_ context.Context, serverID, userName string, upd driver.UserUpdate) error {
	if !m.users.Update(userKey(serverID, userName), func(u driver.User) driver.User {
		u = copyUser(u)
		applyUserUpdate(&u, upd)

		return u
	}) {
		return notFound("user %s does not exist on server %s", userName, serverID)
	}

	return nil
}

// applyUserUpdate mutates u in place from the supplied delta.
func applyUserUpdate(u *driver.User, upd driver.UserUpdate) {
	if upd.Role != nil {
		u.Role = *upd.Role
	}

	if upd.HomeDirectory != nil {
		u.HomeDirectory = *upd.HomeDirectory
	}

	if upd.HomeDirectoryType != nil {
		u.HomeDirectoryType = *upd.HomeDirectoryType
	}

	if upd.HomeDirectoryMappings != nil {
		u.HomeDirectoryMappings = copyHomeDirMappings(upd.HomeDirectoryMappings)
	}

	if upd.Policy != nil {
		u.Policy = *upd.Policy
	}

	if upd.PosixProfile != nil {
		u.PosixProfile = copyPosixProfile(upd.PosixProfile)
	}
}

// DeleteUser removes a user and decrements the server's UserCount.
func (m *Mock) DeleteUser(_ context.Context, serverID, userName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := userKey(serverID, userName)

	u, ok := m.users.Get(key)
	if !ok {
		return notFound("user %s does not exist on server %s", userName, serverID)
	}

	m.users.Delete(key)
	m.deleteTags(u.Arn)
	m.adjustUserCount(serverID, -1)

	return nil
}

// ListUsers returns user summaries for a server sorted by UserName.
func (m *Mock) ListUsers(_ context.Context, serverID string, page driver.Pagination) ([]driver.ListedUser, string, error) {
	if _, ok := m.servers.Get(serverID); !ok {
		return nil, "", notFound("server %s does not exist", serverID)
	}

	keys := sortedKeys(m.userKeysForServer(serverID))
	all := make([]driver.ListedUser, 0, len(keys))

	for _, key := range keys {
		u, ok := m.users.Get(key)
		if !ok {
			continue
		}

		all = append(all, driver.ListedUser{
			Arn:               u.Arn,
			HomeDirectory:     u.HomeDirectory,
			HomeDirectoryType: u.HomeDirectoryType,
			Role:              u.Role,
			//nolint:gosec // key count is bounded by maxSSHKeysPerUser, so it fits int32
			SSHPublicKeyCount: int32(len(u.SSHPublicKeys)),
			UserName:          u.UserName,
		})
	}

	return paginate(all, page)
}

// adjustUserCount adds delta to a server's UserCount, clamped at zero. Callers
// hold m.mu.
func (m *Mock) adjustUserCount(serverID string, delta int32) {
	m.servers.Update(serverID, func(s driver.Server) driver.Server {
		s = copyServer(s)
		s.UserCount += delta

		if s.UserCount < 0 {
			s.UserCount = 0
		}

		return s
	})
}

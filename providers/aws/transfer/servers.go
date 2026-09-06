package transfer

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// maxProtocols is Transfer's cap on the Protocols array (SFTP/FTP/FTPS/AS2).
const maxProtocols = 4

// CreateServer creates a server, materializing the real Transfer defaults and
// returning its generated ServerID.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateServer(_ context.Context, in driver.Server) (string, error) {
	if err := validateServerInput(&in); err != nil {
		return "", err
	}

	applyServerDefaults(&in)

	id := newServerID()
	in.ServerID = id
	in.Arn = m.serverARN(id)
	in.HostKeyFingerprint = hostKeyFingerprint(id)
	in.State = driver.StateOnline
	in.UserCount = 0

	m.servers.Set(id, copyServer(in))

	if len(in.Tags) > 0 {
		m.storeTags(in.Arn, in.Tags)
	}

	return id, nil
}

// validateServerInput rejects invalid enum values and an over-long Protocols
// list before the server is materialized.
func validateServerInput(in *driver.Server) error {
	if in.Domain != "" && in.Domain != driver.DomainS3 && in.Domain != driver.DomainEFS {
		return invalidRequest("invalid Domain %q", in.Domain)
	}

	if len(in.Protocols) > maxProtocols {
		return invalidRequest("at most %d protocols are allowed", maxProtocols)
	}

	for _, p := range in.Protocols {
		if !validProtocol(p) {
			return invalidRequest("invalid protocol %q", p)
		}
	}

	return nil
}

func validProtocol(p string) bool {
	switch p {
	case driver.ProtocolSFTP, driver.ProtocolFTP, driver.ProtocolFTPS, driver.ProtocolAS2:
		return true
	default:
		return false
	}
}

// applyServerDefaults fills the real Transfer defaults into an omitted field so
// DescribeServer echoes concrete values (matching what Terraform reads back).
func applyServerDefaults(in *driver.Server) {
	if in.Domain == "" {
		in.Domain = driver.DomainS3
	}

	if in.EndpointType == "" {
		in.EndpointType = driver.EndpointTypePublic
	}

	if in.IdentityProviderType == "" {
		in.IdentityProviderType = driver.IdentityProviderServiceManaged
	}

	if len(in.Protocols) == 0 {
		in.Protocols = []string{driver.ProtocolSFTP}
	}

	if in.SecurityPolicyName == "" {
		in.SecurityPolicyName = driver.DefaultSecurityPolicyName
	}
}

// DescribeServer returns a deep copy of a server.
func (m *Mock) DescribeServer(_ context.Context, serverID string) (*driver.Server, error) {
	s, ok := m.servers.Get(serverID)
	if !ok {
		return nil, notFound("server %s does not exist", serverID)
	}

	out := copyServer(s)

	return &out, nil
}

// UpdateServer applies the mutable-field delta to a server.
func (m *Mock) UpdateServer(_ context.Context, serverID string, upd driver.ServerUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.servers.Get(serverID)
	if !ok {
		return notFound("server %s does not exist", serverID)
	}

	if upd.Protocols != nil {
		if err := validateServerInput(&driver.Server{Protocols: upd.Protocols}); err != nil {
			return err
		}
	}

	s = copyServer(s)
	applyServerUpdate(&s, upd)
	m.servers.Set(serverID, s)

	return nil
}

// applyServerUpdate mutates s in place from the supplied delta.
func applyServerUpdate(s *driver.Server, upd driver.ServerUpdate) {
	if upd.EndpointType != nil {
		s.EndpointType = *upd.EndpointType
	}

	if upd.EndpointDetails != nil {
		s.EndpointDetails = copyEndpointDetails(upd.EndpointDetails)
	}

	if upd.IdentityProviderDetails != nil {
		s.IdentityProviderDetails = copyStringMap(upd.IdentityProviderDetails)
	}

	if upd.LoggingRole != nil {
		s.LoggingRole = *upd.LoggingRole
	}

	if upd.Protocols != nil {
		s.Protocols = copyStringSlice(upd.Protocols)
	}

	if upd.SecurityPolicyName != nil {
		s.SecurityPolicyName = *upd.SecurityPolicyName
	}

	if upd.Certificate != nil {
		s.Certificate = *upd.Certificate
	}
}

// DeleteServer removes a server and every user hosted on it.
func (m *Mock) DeleteServer(_ context.Context, serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.servers.Get(serverID); !ok {
		return notFound("server %s does not exist", serverID)
	}

	for _, key := range m.userKeysForServer(serverID) {
		if u, ok := m.users.Get(key); ok {
			m.deleteTags(u.Arn)
		}

		m.users.Delete(key)
	}

	m.servers.Delete(serverID)
	m.deleteTags(m.serverARN(serverID))

	return nil
}

// ListServers returns server summaries sorted by ServerID.
func (m *Mock) ListServers(_ context.Context, page driver.Pagination) ([]driver.ListedServer, string, error) {
	keys := sortedKeys(m.servers.Keys())
	all := make([]driver.ListedServer, 0, len(keys))

	for _, id := range keys {
		s, ok := m.servers.Get(id)
		if !ok {
			continue
		}

		all = append(all, driver.ListedServer{
			Arn:                  s.Arn,
			Domain:               s.Domain,
			IdentityProviderType: s.IdentityProviderType,
			EndpointType:         s.EndpointType,
			LoggingRole:          s.LoggingRole,
			ServerID:             s.ServerID,
			State:                s.State,
			UserCount:            s.UserCount,
		})
	}

	return paginate(all, page)
}

// StartServer transitions a server to State ONLINE.
func (m *Mock) StartServer(_ context.Context, serverID string) error {
	return m.setServerState(serverID, driver.StateOnline)
}

// StopServer transitions a server to State OFFLINE.
func (m *Mock) StopServer(_ context.Context, serverID string) error {
	return m.setServerState(serverID, driver.StateOffline)
}

func (m *Mock) setServerState(serverID, state string) error {
	if !m.servers.Update(serverID, func(s driver.Server) driver.Server {
		s = copyServer(s)
		s.State = state

		return s
	}) {
		return notFound("server %s does not exist", serverID)
	}

	return nil
}

// userKeysForServer returns the users store keys hosted on a server. Callers
// hold m.mu.
func (m *Mock) userKeysForServer(serverID string) []string {
	prefix := serverID + userKeySep

	var out []string

	for _, key := range m.users.Keys() {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}

	return out
}

package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// copyConnection returns a deep copy of a connection so a reader cannot alias its
// Tags map.
//
//nolint:gocritic // hugeParam: takes a value to snapshot a copy of stored state.
func copyConnection(c driver.Connection) driver.Connection {
	out := c
	out.Tags = copyTags(c.Tags)

	return out
}

// CreateConnection registers a source-repository connection. Real App Runner
// connections start PENDING_HANDSHAKE until the user completes the OAuth
// handshake; the emulator has no handshake surface, so the connection is created
// PENDING_HANDSHAKE (the SDK-visible initial state) keyed by its server-minted
// ARN. ProviderType is validated against the modeled enum.
func (m *Mock) CreateConnection(
	_ context.Context, name, providerType string, tags map[string]string,
) (*driver.Connection, error) {
	if name == "" {
		return nil, invalidRequest("ConnectionName is required")
	}

	if providerType != driver.ProviderTypeGitHub && providerType != driver.ProviderTypeBitbucket {
		return nil, invalidRequest("ProviderType %q is not one of GITHUB, BITBUCKET", providerType)
	}

	conn := driver.Connection{
		Arn: m.connectionArn(name), Name: name, ProviderType: providerType,
		Status: driver.ConnectionStatusPendingHandshake, CreatedAt: m.now(), Tags: copyTags(tags),
	}

	if !m.connections.SetIfAbsent(conn.Arn, &connectionData{conn: conn}) {
		return nil, invalidRequest("connection ARN collision for %q", conn.Arn)
	}

	out := copyConnection(conn)

	return &out, nil
}

// DeleteConnection moves a connection to DELETED and returns its final state.
func (m *Mock) DeleteConnection(_ context.Context, arn string) (*driver.Connection, error) {
	if arn == "" {
		return nil, invalidRequest("ConnectionArn is required")
	}

	cd, ok := m.connections.Get(arn)
	if !ok {
		return nil, notFound("no connection found for ARN %q", arn)
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.conn.Status = driver.ConnectionStatusDeleted
	out := copyConnection(cd.conn)

	m.connections.Delete(arn)

	return &out, nil
}

func (m *Mock) ListConnections(
	_ context.Context, name, nextToken string, maxResults int32,
) ([]driver.Connection, string, error) {
	all := m.connections.SortedValues()
	out := make([]driver.Connection, 0, len(all))

	for _, cd := range all {
		cd.mu.RLock()
		if name == "" || cd.conn.Name == name {
			out = append(out, copyConnection(cd.conn))
		}
		cd.mu.RUnlock()
	}

	return paginate(out, nextToken, maxResults, func(c driver.Connection) string { return c.Arn })
}

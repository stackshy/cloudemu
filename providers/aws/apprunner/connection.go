package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateConnection provisions a source-code provider connection. A new
// connection is PENDING_HANDSHAKE, matching real App Runner, where the customer
// completes the handshake out of band.
func (m *Mock) CreateConnection(_ context.Context, in *driver.CreateConnectionInput) (*driver.Connection, error) {
	if err := validateConfigName("ConnectionName", in.ConnectionName); err != nil {
		return nil, err
	}

	if in.ProviderType != "GITHUB" && in.ProviderType != "BITBUCKET" {
		return nil, invalidRequest("ProviderType must be GITHUB or BITBUCKET")
	}

	id := newID()
	arn := m.connectionARN(in.ConnectionName, id)

	conn := driver.Connection{
		ConnectionName: in.ConnectionName,
		ConnectionArn:  arn,
		ProviderType:   in.ProviderType,
		Status:         driver.ConnectionStatusPendingHandshake,
		CreatedAt:      m.now(),
		Tags:           copyTags(in.Tags),
	}

	m.connections.Set(arn, conn)

	out := copyConnection(&conn)

	return &out, nil
}

// DeleteConnection removes a connection and returns it with a DELETED status, so
// a subsequent list no longer reports it.
func (m *Mock) DeleteConnection(_ context.Context, arn string) (*driver.Connection, error) {
	conn, ok := m.connections.Get(arn)
	if !ok {
		return nil, notFound("connection %q does not exist", arn)
	}

	m.connections.Delete(arn)

	out := copyConnection(&conn)
	out.Status = driver.ConnectionStatusDeleted

	return &out, nil
}

// ListConnections returns a page of connections, optionally narrowed to a name.
func (m *Mock) ListConnections(
	_ context.Context, name string, page driver.Page,
) ([]*driver.Connection, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	stored := m.connections.SortedValues()
	matched := make([]driver.Connection, 0, len(stored))

	for i := range stored {
		if name == "" || stored[i].ConnectionName == name {
			matched = append(matched, stored[i])
		}
	}

	start, end, next := paginate(len(matched), page)
	out := make([]*driver.Connection, 0, end-start)

	for i := start; i < end; i++ {
		c := copyConnection(&matched[i])
		out = append(out, &c)
	}

	return out, next, nil
}

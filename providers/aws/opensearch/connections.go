package opensearch

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

func copyOutbound(c *driver.OutboundConnection) driver.OutboundConnection { return *c }

func copyInbound(c *driver.InboundConnection) driver.InboundConnection { return *c }

// CreateOutboundConnection creates an outbound cross-cluster connection and a
// matching pending-acceptance inbound connection on the (emulated) remote side.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) CreateOutboundConnection(_ context.Context, in driver.CreateOutboundConnectionInput) (*driver.OutboundConnection, error) {
	if in.ConnectionAlias == "" {
		return nil, validation("ConnectionAlias is required")
	}

	id := idgen.GenerateID("")

	mode := in.ConnectionMode
	if mode == "" {
		mode = "DIRECT"
	}

	out := &driver.OutboundConnection{
		ConnectionID:     id,
		ConnectionAlias:  in.ConnectionAlias,
		ConnectionMode:   mode,
		StatusCode:       driver.ConnStatusPendingAcceptance,
		StatusMessage:    "Connection pending acceptance by remote domain owner.",
		LocalDomainName:  in.LocalDomain.DomainName,
		LocalRegion:      in.LocalDomain.Region,
		LocalOwnerID:     in.LocalDomain.OwnerID,
		RemoteDomainName: in.RemoteDomain.DomainName,
		RemoteRegion:     in.RemoteDomain.Region,
		RemoteOwnerID:    in.RemoteDomain.OwnerID,
	}
	m.outbound.Set(id, out)

	// Mirror as an inbound connection so the recipient side can accept/reject.
	m.inbound.Set(id, &driver.InboundConnection{
		ConnectionID:     id,
		ConnectionStatus: driver.ConnStatusPendingAcceptance,
		StatusMessage:    out.StatusMessage,
		LocalDomainName:  in.RemoteDomain.DomainName,
		LocalRegion:      in.RemoteDomain.Region,
		LocalOwnerID:     in.RemoteDomain.OwnerID,
		RemoteDomainName: in.LocalDomain.DomainName,
		RemoteRegion:     in.LocalDomain.Region,
		RemoteOwnerID:    in.LocalDomain.OwnerID,
		Mode:             mode,
	})

	res := copyOutbound(out)

	return &res, nil
}

// DeleteOutboundConnection deletes an outbound connection.
func (m *Mock) DeleteOutboundConnection(_ context.Context, id string) (*driver.OutboundConnection, error) {
	c, ok := m.outbound.Get(id)
	if !ok {
		return nil, notFound("Outbound connection not found: %s", id)
	}

	out := copyOutbound(c)
	out.StatusCode = driver.ConnStatusDeleting

	m.outbound.Delete(id)

	return &out, nil
}

// DescribeOutboundConnections lists outbound connections, sorted by ID.
func (m *Mock) DescribeOutboundConnections(_ context.Context, page driver.Page) ([]driver.OutboundConnection, string, error) {
	return listStore(m.outbound, copyOutbound, page)
}

// setInboundStatus transitions an inbound connection and mirrors the outbound.
func (m *Mock) setInboundStatus(id, status, message string) (*driver.InboundConnection, error) {
	c, ok := m.inbound.Get(id)
	if !ok {
		return nil, notFound("Inbound connection not found: %s", id)
	}

	out := copyInbound(c)
	out.ConnectionStatus = status
	out.StatusMessage = message
	m.inbound.Set(id, &out)

	if oc, ok := m.outbound.Get(id); ok {
		mirror := copyOutbound(oc)
		mirror.StatusCode = status
		m.outbound.Set(id, &mirror)
	}

	res := copyInbound(&out)

	return &res, nil
}

// AcceptInboundConnection approves a pending inbound connection.
func (m *Mock) AcceptInboundConnection(_ context.Context, id string) (*driver.InboundConnection, error) {
	return m.setInboundStatus(id, driver.ConnStatusActive, "Connection accepted.")
}

// RejectInboundConnection rejects a pending inbound connection.
func (m *Mock) RejectInboundConnection(_ context.Context, id string) (*driver.InboundConnection, error) {
	return m.setInboundStatus(id, driver.ConnStatusRejected, "Connection rejected.")
}

// DeleteInboundConnection deletes an inbound connection.
func (m *Mock) DeleteInboundConnection(_ context.Context, id string) (*driver.InboundConnection, error) {
	c, ok := m.inbound.Get(id)
	if !ok {
		return nil, notFound("Inbound connection not found: %s", id)
	}

	out := copyInbound(c)
	out.ConnectionStatus = driver.ConnStatusDeleting

	m.inbound.Delete(id)

	return &out, nil
}

// DescribeInboundConnections lists inbound connections, sorted by ID.
func (m *Mock) DescribeInboundConnections(_ context.Context, page driver.Page) ([]driver.InboundConnection, string, error) {
	return listStore(m.inbound, copyInbound, page)
}

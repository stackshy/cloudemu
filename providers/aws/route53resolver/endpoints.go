package route53resolver

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// i32 converts a small, bounded slice length to int32 for SDK count fields.
//
//nolint:gosec // resolver endpoint IP-address counts are small and bounded.
func i32(n int) int32 { return int32(n) }

// endpointIDPrefix mirrors real Resolver endpoint IDs: rslvr-in-* for inbound
// (and inbound delegation), rslvr-out-* for outbound.
func endpointIDPrefix(direction string) string {
	if direction == directionInbound || direction == "INBOUND_DELEGATION" {
		return "rslvr-in-"
	}

	return "rslvr-out-"
}

// minEndpointIPs is the minimum number of IP addresses AWS requires a Resolver
// endpoint to retain.
const minEndpointIPs = 2

// hostVPCFor derives the VPC that hosts a Resolver endpoint from its subnets.
// Real Route 53 Resolver reports HostVPCId as the VPC the endpoint's subnets
// belong to; we model subnet→VPC with a stable hash so the same subnet always
// maps to the same VPC id (and a create with no subnet still gets a VPC).
func hostVPCFor(ips []driver.IPAddress) string {
	for i := range ips {
		if ips[i].SubnetID != "" {
			h := fnv.New32a()
			_, _ = h.Write([]byte(ips[i].SubnetID))

			return fmt.Sprintf("vpc-%08x", h.Sum32())
		}
	}

	return idgen.GenerateID("vpc-")
}

func notFound(id string) error {
	return errors.Newf(errors.NotFound, "resolver endpoint %q not found", id)
}

// ipMatches reports whether a stored IP matches a disassociate selector, which
// targets either an explicit IP ID or a subnet (optionally pinned to an IP).
func ipMatches(cur, want *driver.IPAddress) bool {
	if want.IPID != "" {
		return cur.IPID == want.IPID
	}

	return cur.SubnetID == want.SubnetID && (want.IP == "" || cur.IP == want.IP)
}

func (m *Mock) CreateResolverEndpoint(
	_ context.Context, in *driver.CreateResolverEndpointInput,
) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if prior, ok := m.idempotentID("endpoint", in.CreatorRequestID); ok {
		if e, found := m.endpoints.Get(prior); found {
			out := cloneEndpoint(e)

			return &out, nil
		}
	}

	id := idgen.GenerateID(endpointIDPrefix(in.Direction))
	now := m.now()

	ips := make([]driver.IPAddress, 0, len(in.IPAddresses))
	for _, r := range in.IPAddresses {
		ips = append(ips, driver.IPAddress{
			IPID:       idgen.GenerateID("rni-"),
			SubnetID:   r.SubnetID,
			IP:         r.IP,
			IPv6:       r.IPv6,
			Status:     ipStatusAttached,
			CreatedAt:  now,
			ModifiedAt: now,
		})
	}

	epType := in.ResolverEndpointType
	if epType == "" {
		epType = "IPV4"
	}

	e := &driver.ResolverEndpoint{
		ID:                        id,
		ARN:                       m.arn("resolver-endpoint/" + id),
		Name:                      in.Name,
		CreatorRequestID:          in.CreatorRequestID,
		Direction:                 in.Direction,
		HostVPCID:                 hostVPCFor(in.IPAddresses),
		IPAddressCount:            i32(len(ips)),
		SecurityGroupIDs:          append([]string(nil), in.SecurityGroupIDs...),
		IPAddresses:               ips,
		Status:                    statusCreating,
		StatusMessage:             "This Resolver Endpoint is being created.",
		ResolverEndpointType:      epType,
		Protocols:                 append([]string(nil), in.Protocols...),
		OutpostARN:                in.OutpostARN,
		PreferredInstanceType:     in.PreferredInstanceType,
		DNS64Enabled:              in.DNS64Enabled,
		IPv6InternetAccessEnabled: in.IPv6InternetAccessEnabled,
		CreatedAt:                 now,
		ModifiedAt:                now,
	}
	m.endpoints.Set(id, e)
	m.rememberIdempotent("endpoint", in.CreatorRequestID, id)

	if len(in.Tags) > 0 {
		m.tags.Set(e.ARN, copyTags(in.Tags))
	}

	out := cloneEndpoint(e)

	return &out, nil
}

func (m *Mock) GetResolverEndpoint(_ context.Context, id string) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	// Real Route 53 Resolver returns a freshly created endpoint as CREATING and
	// then transitions it to OPERATIONAL. Advance the status on read so the SDK's
	// ResolverEndpointCreated waiter (which polls GetResolverEndpoint) completes.
	if e.Status == statusCreating {
		updated := cloneEndpoint(e)
		updated.Status = statusOperational
		updated.StatusMessage = "This Resolver Endpoint is operational."
		updated.ModifiedAt = m.now()
		m.endpoints.Set(id, &updated)
		e = &updated
	}

	out := cloneEndpoint(e)

	return &out, nil
}

func (m *Mock) UpdateResolverEndpoint(
	_ context.Context, id string, in driver.UpdateResolverEndpointInput,
) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	updated := cloneEndpoint(e)
	if in.Name != nil {
		updated.Name = *in.Name
	}

	if in.ResolverEndpointType != nil {
		updated.ResolverEndpointType = *in.ResolverEndpointType
	}

	if in.Protocols != nil {
		updated.Protocols = append([]string(nil), in.Protocols...)
	}

	updated.ModifiedAt = m.now()
	m.endpoints.Set(id, &updated)

	out := cloneEndpoint(&updated)

	return &out, nil
}

func (m *Mock) DeleteResolverEndpoint(_ context.Context, id string) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	for _, r := range m.rules.All() {
		if r.ResolverEndpointID == id {
			return nil, errors.Newf(errors.FailedPrecondition,
				"resolver endpoint %q still has associated resolver rules", id)
		}
	}

	m.endpoints.Delete(id)
	m.tags.Delete(e.ARN)

	out := cloneEndpoint(e)
	out.Status = statusDeleting
	out.ModifiedAt = m.now()

	return &out, nil
}

func (m *Mock) ListResolverEndpoints(_ context.Context) ([]driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.endpoints.All(), cloneEndpoint), nil
}

func (m *Mock) AssociateResolverEndpointIPAddress(
	_ context.Context, id string, ip *driver.IPAddress,
) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	now := m.now()
	add := driver.IPAddress{
		IPID:       idgen.GenerateID("rni-"),
		SubnetID:   ip.SubnetID,
		IP:         ip.IP,
		IPv6:       ip.IPv6,
		Status:     ipStatusAttached,
		CreatedAt:  now,
		ModifiedAt: now,
	}

	updated := cloneEndpoint(e)
	updated.IPAddresses = append(updated.IPAddresses, add)
	updated.IPAddressCount = i32(len(updated.IPAddresses))
	updated.ModifiedAt = now
	m.endpoints.Set(id, &updated)

	out := cloneEndpoint(&updated)

	return &out, nil
}

func (m *Mock) DisassociateResolverEndpointIPAddress(
	_ context.Context, id string, ip *driver.IPAddress,
) (*driver.ResolverEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	// AWS requires a Resolver endpoint to keep at least two IP addresses; a
	// disassociate that would drop below that minimum is rejected.
	if len(e.IPAddresses) <= minEndpointIPs {
		return nil, errors.Newf(errors.FailedPrecondition,
			"resolver endpoint %q must retain at least %d IP addresses", id, minEndpointIPs)
	}

	updated := cloneEndpoint(e)

	idx := -1

	for i := range updated.IPAddresses {
		if ipMatches(&updated.IPAddresses[i], ip) {
			idx = i

			break
		}
	}

	if idx == -1 {
		return nil, errors.Newf(errors.NotFound, "ip address not found on resolver endpoint %q", id)
	}

	updated.IPAddresses = append(updated.IPAddresses[:idx], updated.IPAddresses[idx+1:]...)
	updated.IPAddressCount = i32(len(updated.IPAddresses))
	updated.ModifiedAt = m.now()
	m.endpoints.Set(id, &updated)

	out := cloneEndpoint(&updated)

	return &out, nil
}

func (m *Mock) ListResolverEndpointIPAddresses(_ context.Context, id string) ([]driver.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.endpoints.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	return append([]driver.IPAddress(nil), e.IPAddresses...), nil
}

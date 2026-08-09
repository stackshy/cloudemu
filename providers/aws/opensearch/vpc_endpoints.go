package opensearch

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// copyVpcEndpoint returns a deep copy of a VPC endpoint (its slices included).
func copyVpcEndpoint(v *driver.VpcEndpoint) driver.VpcEndpoint {
	out := *v
	out.SubnetIDs = copyStrings(v.SubnetIDs)
	out.SecurityGroupIDs = copyStrings(v.SecurityGroupIDs)
	out.AvailabilityZones = copyStrings(v.AvailabilityZones)

	return out
}

// vpcEndpointJSON renders a VPC endpoint as the wire summary map.
//
//nolint:gocritic // hugeParam: called per-endpoint on a copy; a pointer would alias stored state.
func vpcEndpointJSON(v driver.VpcEndpoint) map[string]json.RawMessage {
	vpcOpts, _ := json.Marshal(map[string]any{
		"VPCId":             v.VPCID,
		"SubnetIds":         v.SubnetIDs,
		"SecurityGroupIds":  v.SecurityGroupIDs,
		"AvailabilityZones": v.AvailabilityZones,
	})

	return map[string]json.RawMessage{
		"VpcEndpointId":    rawString(v.VpcEndpointID),
		"VpcEndpointOwner": rawString(v.VpcEndpointOwner),
		"DomainArn":        rawString(v.DomainARN),
		"Status":           rawString(v.Status),
		"Endpoint":         rawString(v.Endpoint),
		"VpcOptions":       json.RawMessage(vpcOpts),
	}
}

// CreateVpcEndpoint creates a VPC endpoint for a domain, immediately ACTIVE.
func (m *Mock) CreateVpcEndpoint(_ context.Context, domainARN string, opts driver.VpcOptions, _ string) (*driver.VpcEndpoint, error) {
	if domainARN == "" {
		return nil, validation("DomainArn is required")
	}

	if _, err := m.getDomain(domainNameFromARN(domainARN)); err != nil {
		return nil, err
	}

	ep := &driver.VpcEndpoint{
		VpcEndpointID:     idgen.GenerateID("aos-"),
		VpcEndpointOwner:  m.opts.AccountID,
		DomainARN:         domainARN,
		Status:            driver.VpcEndpointStatusActive,
		SubnetIDs:         copyStrings(opts.SubnetIDs),
		SecurityGroupIDs:  copyStrings(opts.SecurityGroupIDs),
		VPCID:             idgen.GenerateID("vpc-"),
		AvailabilityZones: []string{m.opts.Region + "a"},
		Endpoint:          "vpc-endpoint-" + idgen.GenerateID("") + "." + m.opts.Region + ".es.amazonaws.com",
	}

	if !m.vpcEnds.SetIfAbsent(ep.VpcEndpointID, ep) {
		return nil, alreadyExists("VPC endpoint already exists: %s", ep.VpcEndpointID)
	}

	out := copyVpcEndpoint(ep)

	return &out, nil
}

// UpdateVpcEndpoint updates a VPC endpoint's subnet/security-group placement.
func (m *Mock) UpdateVpcEndpoint(_ context.Context, id string, opts driver.VpcOptions) (*driver.VpcEndpoint, error) {
	ep, ok := m.vpcEnds.Get(id)
	if !ok {
		return nil, notFound("VPC endpoint not found: %s", id)
	}

	out := copyVpcEndpoint(ep)
	if opts.SubnetIDs != nil {
		out.SubnetIDs = copyStrings(opts.SubnetIDs)
	}

	if opts.SecurityGroupIDs != nil {
		out.SecurityGroupIDs = copyStrings(opts.SecurityGroupIDs)
	}

	m.vpcEnds.Set(id, &out)
	result := copyVpcEndpoint(&out)

	return &result, nil
}

// DeleteVpcEndpoint removes a VPC endpoint, returning its ID and final status.
func (m *Mock) DeleteVpcEndpoint(_ context.Context, id string) (endpointID, status string, err error) {
	ep, ok := m.vpcEnds.Get(id)
	if !ok {
		return "", "", notFound("VPC endpoint not found: %s", id)
	}

	m.vpcEnds.Delete(id)

	return ep.VpcEndpointID, driver.VpcEndpointStatusDeleting, nil
}

// DescribeVpcEndpoints returns the named endpoints and an error list for any
// that were not found.
func (m *Mock) DescribeVpcEndpoints(_ context.Context, ids []string) ([]driver.VpcEndpoint, []map[string]json.RawMessage, error) {
	found := make([]driver.VpcEndpoint, 0, len(ids))
	errs := make([]map[string]json.RawMessage, 0)

	for _, id := range ids {
		ep, ok := m.vpcEnds.Get(id)
		if !ok {
			errs = append(errs, map[string]json.RawMessage{
				"VpcEndpointId": rawString(id),
				"ErrorCode":     rawString("ENDPOINT_NOT_FOUND"),
				"ErrorMessage":  rawString("VPC endpoint not found: " + id),
			})

			continue
		}

		found = append(found, copyVpcEndpoint(ep))
	}

	return found, errs, nil
}

// ListVpcEndpoints lists all VPC endpoint summaries, paginated.
func (m *Mock) ListVpcEndpoints(
	_ context.Context, page driver.Page,
) (endpoints []map[string]json.RawMessage, nextToken string, err error) {
	return m.listVpcEndpoints(func(*driver.VpcEndpoint) bool { return true }, page), "", nil
}

// ListVpcEndpointsForDomain lists VPC endpoints attached to a domain.
func (m *Mock) ListVpcEndpointsForDomain(
	_ context.Context, domainName string, page driver.Page,
) (endpoints []map[string]json.RawMessage, nextToken string, err error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, "", err
	}

	arn := m.domainARN(domainName)

	return m.listVpcEndpoints(func(v *driver.VpcEndpoint) bool {
		return v.DomainARN == arn
	}, page), "", nil
}

// listVpcEndpoints returns matching endpoint summaries sorted by ID.
func (m *Mock) listVpcEndpoints(match func(*driver.VpcEndpoint) bool, page driver.Page) []map[string]json.RawMessage {
	ids := m.vpcEnds.Keys()
	sort.Strings(ids)

	out := make([]map[string]json.RawMessage, 0, len(ids))

	for _, id := range ids {
		if ep, ok := m.vpcEnds.Get(id); ok && match(ep) {
			out = append(out, vpcEndpointJSON(copyVpcEndpoint(ep)))
		}
	}

	start, end, _ := paginate(len(out), page)

	return out[start:end]
}

// AuthorizeVpcEndpointAccess authorizes an account (or service) to create a
// VPC endpoint for the domain, returning the authorized-principal record.
func (m *Mock) AuthorizeVpcEndpointAccess(_ context.Context, domainName, account, service string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	principalType := "AWS_ACCOUNT"
	principal := account

	if service != "" {
		principalType = "AWS_SERVICE"
		principal = service
	}

	principalJSON, _ := json.Marshal(map[string]any{
		"PrincipalType": principalType,
		"Principal":     principal,
	})

	return map[string]json.RawMessage{
		"AuthorizedPrincipal": json.RawMessage(principalJSON),
	}, nil
}

// RevokeVpcEndpointAccess revokes a previously authorized principal.
func (m *Mock) RevokeVpcEndpointAccess(_ context.Context, domainName, _, _ string) error {
	_, err := m.getDomain(domainName)

	return err
}

// ListVpcEndpointAccess lists the principals authorized for a domain (empty).
func (m *Mock) ListVpcEndpointAccess(
	_ context.Context, domainName string, _ driver.Page,
) (access []map[string]json.RawMessage, nextToken string, err error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, "", err
	}

	return []map[string]json.RawMessage{}, "", nil
}

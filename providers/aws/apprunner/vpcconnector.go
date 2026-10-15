package apprunner

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateVpcConnector provisions a VPC connector. Reusing a name mints the next
// incremental revision, matching real App Runner (VPC connectors are immutable
// and versioned by name).
func (m *Mock) CreateVpcConnector(ctx context.Context, in *driver.CreateVpcConnectorInput) (*driver.VpcConnector, error) {
	if err := validateResourceName("VpcConnectorName", in.VpcConnectorName); err != nil {
		return nil, err
	}

	if len(in.Subnets) == 0 {
		return nil, invalidRequest("at least one subnet is required")
	}

	if err := m.checkConnectorNetwork(ctx, in.Subnets, in.SecurityGroups); err != nil {
		return nil, err
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	revision := m.nextVpcConnectorRevision(in.VpcConnectorName)
	id := newID()
	arn := m.vpcConnectorARN(in.VpcConnectorName, revision, id)

	conn := driver.VpcConnector{
		VpcConnectorName:     in.VpcConnectorName,
		VpcConnectorArn:      arn,
		VpcConnectorRevision: revision,
		Subnets:              copyStrings(in.Subnets),
		SecurityGroups:       copyStrings(in.SecurityGroups),
		Status:               driver.ResourceStatusActive,
		CreatedAt:            m.now(),
		Tags:                 copyTags(in.Tags),
	}

	m.vpcConnectors.Set(arn, conn)

	out := copyVpcConnector(&conn)

	return &out, nil
}

// checkConnectorNetwork cross-checks a connector's subnets and security groups
// against the networking mock when one is wired: every subnet and security group
// must exist, all subnets must belong to one VPC and the security groups to that
// same VPC. Without a resolver the ids are accepted as given.
func (m *Mock) checkConnectorNetwork(ctx context.Context, subnets, groups []string) error {
	if m.network == nil {
		return nil
	}

	infos, err := m.network.DescribeSubnets(ctx, subnets)
	if err != nil {
		return invalidRequest("subnet lookup failed: " + cerrors.Message(err))
	}

	vpcID := ""

	for i := range infos {
		if vpcID == "" {
			vpcID = infos[i].VPCID
		}

		if infos[i].VPCID != vpcID {
			return invalidRequest("all subnets must belong to the same VPC")
		}
	}

	if len(groups) == 0 {
		return nil
	}

	sgs, err := m.network.DescribeSecurityGroups(ctx, groups)
	if err != nil {
		return invalidRequest("security group lookup failed: " + cerrors.Message(err))
	}

	for i := range sgs {
		if sgs[i].VPCID != vpcID {
			return invalidRequest("security group " + sgs[i].ID + " does not belong to the VPC of the subnets")
		}
	}

	return nil
}

// nextVpcConnectorRevision returns one past the highest existing revision of name.
func (m *Mock) nextVpcConnectorRevision(name string) int32 {
	highest := int32(0)
	all := m.vpcConnectors.All()

	for arn := range all {
		c := all[arn]
		if c.VpcConnectorName == name && c.VpcConnectorRevision > highest {
			highest = c.VpcConnectorRevision
		}
	}

	return m.nextRevision("vpcconnector", name, highest)
}

// vpcConnectorInUse reports whether any service uses the connector for egress.
func (m *Mock) vpcConnectorInUse(arn string) bool {
	svcs := m.services.SortedValues()
	for i := range svcs {
		n := svcs[i].NetworkConfiguration
		if n != nil && n.EgressConfiguration != nil && n.EgressConfiguration.VpcConnectorArn == arn {
			return true
		}
	}

	return false
}

// DescribeVpcConnector returns the VPC connector by ARN.
func (m *Mock) DescribeVpcConnector(_ context.Context, arn string) (*driver.VpcConnector, error) {
	conn, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return nil, notFound("VPC connector %q does not exist", arn)
	}

	out := copyVpcConnector(&conn)

	return &out, nil
}

// DeleteVpcConnector removes a VPC connector and returns it with an INACTIVE
// status, so a subsequent describe 404s. A connector that a service uses for
// egress is rejected with an InvalidRequestException, as in real App Runner.
func (m *Mock) DeleteVpcConnector(_ context.Context, arn string) (*driver.VpcConnector, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	conn, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return nil, notFound("VPC connector %q does not exist", arn)
	}

	if m.vpcConnectorInUse(arn) {
		return nil, invalidRequest("VPC connector is used by one or more App Runner services")
	}

	m.vpcConnectors.Delete(arn)

	out := copyVpcConnector(&conn)
	out.Status = driver.ResourceStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// ListVpcConnectors returns a deterministic page of VPC connectors ordered by ARN.
func (m *Mock) ListVpcConnectors(_ context.Context, page driver.Page) ([]*driver.VpcConnector, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	stored := m.vpcConnectors.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.VpcConnector, 0, end-start)

	for i := start; i < end; i++ {
		c := copyVpcConnector(&stored[i])
		out = append(out, &c)
	}

	return out, next, nil
}

package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateVpcConnector provisions a VPC connector. Reusing a name mints the next
// incremental revision, matching real App Runner (VPC connectors are immutable
// and versioned by name).
func (m *Mock) CreateVpcConnector(_ context.Context, in *driver.CreateVpcConnectorInput) (*driver.VpcConnector, error) {
	if in.VpcConnectorName == "" {
		return nil, invalidRequest("VpcConnectorName is required")
	}

	if len(in.Subnets) == 0 {
		return nil, invalidRequest("at least one subnet is required")
	}

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

	return highest + 1
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
// status, so a subsequent describe 404s.
func (m *Mock) DeleteVpcConnector(_ context.Context, arn string) (*driver.VpcConnector, error) {
	conn, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return nil, notFound("VPC connector %q does not exist", arn)
	}

	m.vpcConnectors.Delete(arn)

	out := copyVpcConnector(&conn)
	out.Status = driver.ResourceStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// ListVpcConnectors returns a deterministic page of VPC connectors ordered by ARN.
func (m *Mock) ListVpcConnectors(_ context.Context, page driver.Page) ([]*driver.VpcConnector, string, error) {
	stored := m.vpcConnectors.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.VpcConnector, 0, end-start)

	for i := start; i < end; i++ {
		c := copyVpcConnector(&stored[i])
		out = append(out, &c)
	}

	return out, next, nil
}

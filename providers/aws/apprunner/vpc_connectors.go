package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateVpcConnector registers a VPC connector. It comes up ACTIVE immediately
// and is keyed by its server-minted ARN (SetIfAbsent enforces uniqueness). The
// Subnets and SecurityGroups slices are deep-copied so callers can't mutate
// stored state.
func (m *Mock) CreateVpcConnector(
	_ context.Context, name string, subnets, securityGroups []string, tags map[string]string,
) (*driver.VpcConnector, error) {
	if name == "" {
		return nil, invalidRequest("VpcConnectorName is required")
	}

	if len(subnets) == 0 {
		return nil, invalidRequest("Subnets must contain at least one subnet")
	}

	vc := driver.VpcConnector{
		Arn: m.vpcConnectorArn(name, 1), Name: name, Revision: 1,
		Status: driver.StatusActive, Subnets: copyStrings(subnets),
		SecurityGroups: copyStrings(securityGroups), CreatedAt: m.now(), Tags: copyTags(tags),
	}

	if !m.vpcConnectors.SetIfAbsent(vc.Arn, &vpcConnectorData{vc: vc}) {
		return nil, invalidRequest("VPC connector ARN collision for %q", vc.Arn)
	}

	return copyVpcConnector(&vc), nil
}

func (m *Mock) DescribeVpcConnector(_ context.Context, arn string) (*driver.VpcConnector, error) {
	vd, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return nil, notFound("no VPC connector found for ARN %q", arn)
	}

	vd.mu.RLock()
	defer vd.mu.RUnlock()

	return copyVpcConnector(&vd.vc), nil
}

// DeleteVpcConnector marks a connector INACTIVE and stamps its deletion time.
func (m *Mock) DeleteVpcConnector(_ context.Context, arn string) (*driver.VpcConnector, error) {
	vd, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return nil, notFound("no VPC connector found for ARN %q", arn)
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	vd.vc.Status = driver.StatusInactive
	if vd.vc.DeletedAt.IsZero() {
		vd.vc.DeletedAt = m.now()
	}

	return copyVpcConnector(&vd.vc), nil
}

func (m *Mock) ListVpcConnectors(
	_ context.Context, nextToken string, maxResults int32,
) ([]driver.VpcConnector, string, error) {
	all := m.vpcConnectors.SortedValues()
	out := make([]driver.VpcConnector, 0, len(all))

	for _, vd := range all {
		vd.mu.RLock()
		out = append(out, *copyVpcConnector(&vd.vc))
		vd.mu.RUnlock()
	}

	return paginate(out, nextToken, maxResults, func(v driver.VpcConnector) string { return v.Arn })
}

// copyVpcConnector deep-copies a connector, including its Subnets and
// SecurityGroups slices.
func copyVpcConnector(v *driver.VpcConnector) *driver.VpcConnector {
	out := *v
	out.Subnets = copyStrings(v.Subnets)
	out.SecurityGroups = copyStrings(v.SecurityGroups)
	out.Tags = copyTags(v.Tags)

	return &out
}

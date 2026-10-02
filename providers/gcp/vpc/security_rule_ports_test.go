package vpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestSecurityRulePortValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	network, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "fw", VPCID: network.ID})
	require.NoError(t, err)

	bad := []driver.SecurityRule{
		{Protocol: "tcp", FromPort: 0, ToPort: 99999, CIDR: "10.0.0.0/16"},
		{Protocol: "udp", FromPort: 53, ToPort: 10, CIDR: "10.0.0.0/16"},
	}

	for _, r := range bad {
		assert.True(t, cerrors.IsInvalidArgument(m.AddIngressRule(ctx, sg.ID, r)), "ingress %+v", r)
		assert.True(t, cerrors.IsInvalidArgument(m.AddEgressRule(ctx, sg.ID, r)), "egress %+v", r)
	}

	// ICMP and all-protocol rules carry no ports, so -1 is fine.
	require.NoError(t, m.AddEgressRule(ctx, sg.ID, driver.SecurityRule{Protocol: "icmp", FromPort: -1, ToPort: -1, CIDR: "10.0.0.0/16"}))
	require.NoError(t, m.AddEgressRule(ctx, sg.ID, driver.SecurityRule{Protocol: "-1", FromPort: -1, ToPort: -1, CIDR: "10.9.0.0/16"}))

	require.NoError(t, m.AddIngressRule(ctx, sg.ID, driver.SecurityRule{Protocol: "tcp", FromPort: 22, ToPort: 22, CIDR: "10.0.0.0/16"}))

	sgs, err := m.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	assert.Len(t, sgs[0].IngressRules, 1)
}

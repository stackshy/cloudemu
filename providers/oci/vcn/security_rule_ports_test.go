package vcn_test

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
	m := newMock(t)
	parent := newVCN(t, m, vcnCIDR)

	nsg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg", VPCID: parent.ID})
	require.NoError(t, err)

	bad := []driver.SecurityRule{
		{Protocol: "tcp", FromPort: 0, ToPort: 99999, CIDR: "10.0.0.0/16"},
		{Protocol: "tcp", FromPort: 443, ToPort: 80, CIDR: "10.0.0.0/16"},
	}

	for _, r := range bad {
		assert.True(t, cerrors.IsInvalidArgument(m.AddIngressRule(ctx, nsg.ID, r)), "ingress %+v", r)
		assert.True(t, cerrors.IsInvalidArgument(m.AddEgressRule(ctx, nsg.ID, r)), "egress %+v", r)
	}

	// ICMP and all-protocol rules carry no ports, so -1 is fine.
	require.NoError(t, m.AddEgressRule(ctx, nsg.ID, driver.SecurityRule{Protocol: "icmp", FromPort: -1, ToPort: -1, CIDR: "10.0.0.0/16"}))
	require.NoError(t, m.AddEgressRule(ctx, nsg.ID, driver.SecurityRule{Protocol: "-1", FromPort: -1, ToPort: -1, CIDR: "10.9.0.0/16"}))

	require.NoError(t, m.AddIngressRule(ctx, nsg.ID, driver.SecurityRule{Protocol: "tcp", FromPort: 22, ToPort: 22, CIDR: "10.0.0.0/16"}))
}

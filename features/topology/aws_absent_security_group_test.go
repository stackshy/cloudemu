package topology

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// countingNetworking records the network ACL lookups the connectivity engine
// makes, so a provider that has none can prove it stays that way.
type countingNetworking struct {
	netdriver.Networking

	aclLookups int
}

func (n *countingNetworking) DescribeNetworkACLs(ctx context.Context, ids []string) ([]netdriver.NetworkACL, error) {
	n.aclLookups++

	return n.Networking.DescribeNetworkACLs(ctx, ids)
}

// TestCanConnectAWSAbsentSecurityGroupUnchanged pins the AWS path against the
// security-list fall-through added for OCI: an instance referencing a deleted
// security group is denied for want of an ingress rule, exactly as before, and
// the id is never tried as a network ACL — VPC's DescribeSecurityGroups fails
// the whole lookup on an unknown id, so nothing falls through.
func TestCanConnectAWSAbsentSecurityGroupUnchanged(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	counter := &countingNetworking{Networking: vpcMock}
	engine.networking = counter

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", true)

	// An ACL that would allow everything if the deleted group's id were ever
	// resolved as one.
	acl, err := vpcMock.CreateNetworkACL(ctx, vpcID, nil)
	require.NoError(t, err)
	require.NoError(t, vpcMock.AddNetworkACLRule(ctx, acl.ID, &netdriver.NetworkACLRule{
		RuleNumber: 100, Protocol: "-1", CIDR: "0.0.0.0/0", Action: "allow",
	}))

	src, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(src[0].ID, vpcID))

	dst, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(dst[0].ID, vpcID))

	query := ConnectivityQuery{
		SrcInstanceID: src[0].ID,
		DstInstanceID: dst[0].ID,
		Port:          443,
		Protocol:      "tcp",
	}

	// Baseline: the destination group allows the traffic.
	result, err := engine.CanConnect(ctx, query)
	require.NoError(t, err)
	assert.True(t, result.Allowed, result.Reason)

	// The instance now references a group that no longer exists.
	require.NoError(t, vpcMock.DeleteSecurityGroup(ctx, dstSGID))

	result, err = engine.CanConnect(ctx, query)
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Contains(t, result.Reason, "no ingress rule")
	assert.Nil(t, result.SGVerdict.IngressMatch)

	assert.Zero(t, counter.aclLookups, "the AWS path must not consult network ACLs")
}

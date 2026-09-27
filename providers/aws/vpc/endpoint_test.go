package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// countENIsInSubnet returns how many network interfaces reside in the subnet.
func countENIsInSubnet(m *Mock, subnetID string) int {
	n := 0

	for _, eni := range m.enis.All() {
		if eni.SubnetID == subnetID {
			n++
		}
	}

	return n
}

// TestInterfaceEndpointBacksENIs pins that an Interface-type VPC endpoint provisions
// one ENI per specified subnet, that DescribeVpcEndpoints returns their ids, and that
// deleting the endpoint releases them.
func TestInterfaceEndpointBacksENIs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	subA, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)
	subB, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.2.0/24"})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID:        v.ID,
		ServiceName:  "com.amazonaws.us-east-1.s3",
		EndpointType: "Interface",
		SubnetIDs:    []string{subA.ID, subB.ID},
	})
	requireNoError(t, err)

	assertEqual(t, 2, len(ep.NetworkInterfaceIDs))
	assertEqual(t, 1, countENIsInSubnet(m, subA.ID))
	assertEqual(t, 1, countENIsInSubnet(m, subB.ID))

	// Describe reflects the ENI ids too.
	got, err := m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)
	assertEqual(t, 2, len(got[0].NetworkInterfaceIDs))

	// Deleting the endpoint releases its backing ENIs.
	requireNoError(t, m.DeleteVPCEndpoint(ctx, ep.ID))
	assertEqual(t, 0, countENIsInSubnet(m, subA.ID))
	assertEqual(t, 0, countENIsInSubnet(m, subB.ID))
}

// TestGatewayEndpointHasNoENIs pins that a Gateway-type endpoint provisions no
// interfaces, matching real EC2 (gateway endpoints are route-table entries).
func TestGatewayEndpointHasNoENIs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID:        v.ID,
		ServiceName:  "com.amazonaws.us-east-1.s3",
		EndpointType: "Gateway",
		SubnetIDs:    []string{sub.ID},
	})
	requireNoError(t, err)

	assertEqual(t, 0, len(ep.NetworkInterfaceIDs))
	assertEqual(t, 0, countENIsInSubnet(m, sub.ID))
}

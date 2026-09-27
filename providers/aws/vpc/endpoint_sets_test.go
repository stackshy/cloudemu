package vpc

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func newGatewayEndpoint(t *testing.T, m *Mock, vpcID, service string, rts ...string) *driver.VPCEndpoint {
	t.Helper()

	ep, err := m.CreateVPCEndpoint(context.Background(), driver.VPCEndpointConfig{
		VPCID: vpcID, ServiceName: service, EndpointType: "Gateway", RouteTableIDs: rts,
	})
	requireNoError(t, err)

	return ep
}

func newRouteTables(t *testing.T, m *Mock, vpcID string, n int) []string {
	t.Helper()

	ids := make([]string, 0, n)

	for range n {
		rt, err := m.CreateRouteTable(context.Background(), driver.RouteTableConfig{VPCID: vpcID})
		requireNoError(t, err)

		ids = append(ids, rt.ID)
	}

	return ids
}

// TestModifyVPCEndpointSetsConcurrent pins that concurrent AddRouteTableId
// changes on one endpoint all land. Each change is applied as a delta under the
// provider lock, so no caller's read-modify-write drops another's table.
func TestModifyVPCEndpointSetsConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)
	rts := newRouteTables(t, m, v.ID, 12)
	ep := newGatewayEndpoint(t, m, v.ID, "com.amazonaws.us-east-1.s3")

	var wg sync.WaitGroup

	for _, rt := range rts {
		wg.Add(1)

		go func(rt string) {
			defer wg.Done()

			_, err := m.ModifyVPCEndpointSets(ctx, ep.ID, &driver.VPCEndpointSetChange{AddRouteTableIDs: []string{rt}})
			if err != nil {
				t.Errorf("add %s: %v", rt, err)
			}
		}(rt)
	}

	wg.Wait()

	got, err := m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)
	assertEqual(t, len(rts), len(got[0].RouteTableIDs))

	for _, rt := range rts {
		assertEqual(t, 1, len(routesTo(t, m, rt, ep.ID)))
	}

	for _, rt := range rts {
		wg.Add(1)

		go func(rt string) {
			defer wg.Done()

			if _, err := m.ModifyVPCEndpointSets(ctx, ep.ID, &driver.VPCEndpointSetChange{RemoveRouteTableIDs: []string{rt}}); err != nil {
				t.Errorf("remove %s: %v", rt, err)
			}
		}(rt)
	}

	wg.Wait()

	got, err = m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)
	assertEqual(t, 0, len(got[0].RouteTableIDs))

	for _, rt := range rts {
		assertEqual(t, 0, len(routesTo(t, m, rt, ep.ID)))
	}
}

// TestModifyVPCEndpointSetsSubnetsAndGroups covers the subnet and security
// group deltas: an Interface endpoint gains or loses one ENI per subnet.
func TestModifyVPCEndpointSetsSubnetsAndGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	subA, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)
	subB, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.2.0/24"})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.ssm", EndpointType: "Interface",
		SubnetIDs: []string{subA.ID}, SecurityGroupIDs: []string{"sg-a"},
	})
	requireNoError(t, err)

	out, err := m.ModifyVPCEndpointSets(ctx, ep.ID, &driver.VPCEndpointSetChange{
		AddSubnetIDs: []string{subB.ID}, RemoveSubnetIDs: []string{subA.ID},
		AddSecurityGroupIDs: []string{"sg-b"}, RemoveSecurityGroupIDs: []string{"sg-a"},
	})
	requireNoError(t, err)

	assertEqual(t, 1, len(out.SubnetIDs))
	assertEqual(t, subB.ID, out.SubnetIDs[0])
	assertEqual(t, 1, len(out.SecurityGroupIDs))
	assertEqual(t, "sg-b", out.SecurityGroupIDs[0])
	assertEqual(t, 1, len(out.NetworkInterfaceIDs))
	assertEqual(t, 0, countENIsInSubnet(m, subA.ID))
	assertEqual(t, 1, countENIsInSubnet(m, subB.ID))

	if _, err := m.ModifyVPCEndpointSets(ctx, "vpce-missing", &driver.VPCEndpointSetChange{}); !errors.IsNotFound(err) {
		t.Fatalf("unknown endpoint: want NotFound, got %v", err)
	}
}

// TestGatewayEndpointOneRoutePerServicePerTable pins the EC2 rule that a route
// table holds at most one endpoint route per service: a second s3 endpoint on
// the same table is refused with AlreadyExists, on create and on modify.
func TestGatewayEndpointOneRoutePerServicePerTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)
	rts := newRouteTables(t, m, v.ID, 2)

	first := newGatewayEndpoint(t, m, v.ID, "com.amazonaws.us-east-1.s3", rts[0])

	_, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.s3", EndpointType: "Gateway", RouteTableIDs: []string{rts[0]},
	})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("second s3 endpoint on the same table: want AlreadyExists, got %v", err)
	}

	eps, err := m.DescribeVPCEndpoints(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(eps))

	// dynamodb on the same table is fine, and so is s3 on another table.
	newGatewayEndpoint(t, m, v.ID, "com.amazonaws.us-east-1.dynamodb", rts[0])
	second := newGatewayEndpoint(t, m, v.ID, "com.amazonaws.us-east-1.s3", rts[1])

	_, err = m.ModifyVPCEndpointSets(ctx, second.ID, &driver.VPCEndpointSetChange{AddRouteTableIDs: []string{rts[0]}})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("modify adding a table with an s3 route: want AlreadyExists, got %v", err)
	}

	assertEqual(t, 1, len(routesTo(t, m, rts[0], first.ID)))
	assertEqual(t, 0, len(routesTo(t, m, rts[0], second.ID)))
}

// TestDeleteRouteTableLeavesGatewayEndpoint pins that deleting a route table
// drops it from the endpoints that used it.
func TestDeleteRouteTableLeavesGatewayEndpoint(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)
	rts := newRouteTables(t, m, v.ID, 2)
	ep := newGatewayEndpoint(t, m, v.ID, "com.amazonaws.us-east-1.s3", rts...)

	requireNoError(t, m.DeleteRouteTable(ctx, rts[0]))

	got, err := m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)
	assertEqual(t, 1, len(got[0].RouteTableIDs))
	assertEqual(t, rts[1], got[0].RouteTableIDs[0])
}

// TestCreateDeleteEndpointConcurrentRoutes runs creates and deletes in
// parallel and checks that no endpoint route outlives its endpoint.
func TestCreateDeleteEndpointConcurrentRoutes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)
	rts := newRouteTables(t, m, v.ID, 8)

	var wg sync.WaitGroup

	for _, rt := range rts {
		wg.Add(1)

		go func(rt string) {
			defer wg.Done()

			ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
				VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.s3", EndpointType: "Gateway", RouteTableIDs: []string{rt},
			})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}

			if err := m.DeleteVPCEndpoint(ctx, ep.ID); err != nil {
				t.Errorf("delete: %v", err)
			}
		}(rt)
	}

	wg.Wait()

	tables, err := m.DescribeRouteTables(ctx, rts)
	requireNoError(t, err)

	for _, rt := range tables {
		for _, r := range rt.Routes {
			if r.DestinationPrefixListID != "" {
				t.Errorf("orphan endpoint route %+v in %s", r, rt.ID)
			}
		}
	}
}

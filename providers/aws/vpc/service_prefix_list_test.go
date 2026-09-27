package vpc

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestServicePrefixListsPerRegion pins that DescribePrefixLists returns the
// AWS-managed s3 and dynamodb lists for the region asked about, with pl- ids
// that stay the same across calls and differ between regions.
func TestServicePrefixListsPerRegion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	east, err := m.DescribePrefixLists(ctx, "us-east-1", nil)
	requireNoError(t, err)
	assertEqual(t, 2, len(east))

	names := map[string]string{}

	for _, pl := range east {
		if !strings.HasPrefix(pl.ID, "pl-") {
			t.Errorf("prefix list id %q lacks the pl- prefix", pl.ID)
		}

		if len(pl.CIDRs) == 0 {
			t.Errorf("prefix list %s has no cidrs", pl.Name)
		}

		names[pl.Name] = pl.ID
	}

	if names["com.amazonaws.us-east-1.s3"] == "" || names["com.amazonaws.us-east-1.dynamodb"] == "" {
		t.Fatalf("missing s3/dynamodb lists, got %v", names)
	}

	again, err := m.DescribePrefixLists(ctx, "us-east-1", nil)
	requireNoError(t, err)

	for _, pl := range again {
		assertEqual(t, names[pl.Name], pl.ID)
	}

	west, err := m.DescribePrefixLists(ctx, "us-west-2", nil)
	requireNoError(t, err)

	for _, pl := range west {
		if !strings.HasPrefix(pl.Name, "com.amazonaws.us-west-2.") {
			t.Errorf("us-west-2 list named %q", pl.Name)
		}

		if pl.ID == names["com.amazonaws.us-east-1.s3"] || pl.ID == names["com.amazonaws.us-east-1.dynamodb"] {
			t.Errorf("us-west-2 list %s reuses a us-east-1 id", pl.ID)
		}
	}
}

// TestServicePrefixListsByID pins id lookup and the NotFound for an unknown id.
func TestServicePrefixListsByID(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	all, err := m.DescribePrefixLists(ctx, "", nil)
	requireNoError(t, err)

	one, err := m.DescribePrefixLists(ctx, "", []string{all[0].ID})
	requireNoError(t, err)
	assertEqual(t, 1, len(one))
	assertEqual(t, all[0].Name, one[0].Name)

	_, err = m.DescribePrefixLists(ctx, "", []string{"pl-00000000"})
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown id: want NotFound, got %v", err)
	}
}

// TestAWSManagedPrefixListsShape pins the managed-prefix-list view of the
// service lists: owner AWS, create-complete, entries equal to the cidrs.
func TestAWSManagedPrefixListsShape(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	svc, err := m.DescribePrefixLists(ctx, "us-east-1", nil)
	requireNoError(t, err)

	managed, err := m.DescribeAWSManagedPrefixLists(ctx, "us-east-1", nil)
	requireNoError(t, err)
	assertEqual(t, len(svc), len(managed))

	for i := range managed {
		pl := managed[i]
		assertEqual(t, "AWS", pl.OwnerID)
		assertEqual(t, "create-complete", pl.State)
		assertEqual(t, "IPv4", pl.AddressFamily)
		assertEqual(t, svc[i].ID, pl.ID)
		assertEqual(t, len(svc[i].CIDRs), len(pl.Entries))
		// AWS-owned lists carry no maxEntries or version on the wire.
		assertEqual(t, 0, pl.MaxEntries)
		assertEqual(t, 0, pl.Version)
	}

	none, err := m.DescribeAWSManagedPrefixLists(ctx, "us-east-1", []string{"pl-00000000"})
	requireNoError(t, err)
	assertEqual(t, 0, len(none))
}

// routesTo returns the routes in rtID whose target is targetID.
func routesTo(t *testing.T, m *Mock, rtID, targetID string) []driver.Route {
	t.Helper()

	rts, err := m.DescribeRouteTables(context.Background(), []string{rtID})
	requireNoError(t, err)

	var out []driver.Route

	for _, r := range rts[0].Routes {
		if r.TargetID == targetID {
			out = append(out, r)
		}
	}

	return out
}

// TestGatewayEndpointPrefixListRoutes pins that a Gateway endpoint adds a
// DestinationPrefixListId route to each of its route tables, pointing at the
// service's pl- id, and that modify and delete take the routes away again.
func TestGatewayEndpointPrefixListRoutes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	rtA, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)
	rtB, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)

	pls, err := m.DescribePrefixLists(ctx, "us-east-1", nil)
	requireNoError(t, err)

	var s3ID string

	for _, pl := range pls {
		if pl.Name == "com.amazonaws.us-east-1.s3" {
			s3ID = pl.ID
		}
	}

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.s3",
		EndpointType: "Gateway", RouteTableIDs: []string{rtA.ID, rtB.ID},
	})
	requireNoError(t, err)

	for _, rt := range []string{rtA.ID, rtB.ID} {
		got := routesTo(t, m, rt, ep.ID)
		assertEqual(t, 1, len(got))
		assertEqual(t, s3ID, got[0].DestinationPrefixListID)
		assertEqual(t, "", got[0].DestinationCIDR)
		assertEqual(t, "active", got[0].State)
	}

	_, err = m.ModifyVPCEndpoint(ctx, ep.ID, driver.VPCEndpointConfig{RouteTableIDs: []string{rtA.ID}})
	requireNoError(t, err)
	assertEqual(t, 1, len(routesTo(t, m, rtA.ID, ep.ID)))
	assertEqual(t, 0, len(routesTo(t, m, rtB.ID, ep.ID)))

	requireNoError(t, m.DeleteVPCEndpoint(ctx, ep.ID))
	assertEqual(t, 0, len(routesTo(t, m, rtA.ID, ep.ID)))
}

// TestInterfaceEndpointAddsNoRoutes pins that only Gateway endpoints touch
// route tables.
func TestInterfaceEndpointAddsNoRoutes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.ssm",
		EndpointType: "Interface", RouteTableIDs: []string{rt.ID},
	})
	requireNoError(t, err)
	assertEqual(t, 0, len(routesTo(t, m, rt.ID, ep.ID)))
}

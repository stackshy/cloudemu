package ec2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// mkVPCAndSubnet returns a VPC id and a subnet id inside it.
func mkVPCAndSubnet(t *testing.T, h *Handler) (vpcID, subnetID string) {
	t.Helper()

	vpcResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateVpc"}, "CidrBlock": {"10.0.0.0/16"},
	})
	if vpcResp.Code != http.StatusOK {
		t.Fatalf("CreateVpc = %d: %s", vpcResp.Code, vpcResp.Body.String())
	}

	vpcID = between(vpcResp.Body.String(), "<vpcId>", "</vpcId>")

	subResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateSubnet"}, "VpcId": {vpcID}, "CidrBlock": {"10.0.1.0/24"},
	})
	if subResp.Code != http.StatusOK {
		t.Fatalf("CreateSubnet = %d: %s", subResp.Code, subResp.Body.String())
	}

	subnetID = between(subResp.Body.String(), "<subnetId>", "</subnetId>")

	if vpcID == "" || subnetID == "" {
		t.Fatalf("missing ids: vpc=%q subnet=%q", vpcID, subnetID)
	}

	return vpcID, subnetID
}

func mkRouteTable(t *testing.T, h *Handler, vpcID string) string {
	t.Helper()

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateRouteTable"}, "VpcId": {vpcID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("CreateRouteTable = %d: %s", resp.Code, resp.Body.String())
	}

	rtID := between(resp.Body.String(), "<routeTableId>", "</routeTableId>")
	if rtID == "" {
		t.Fatalf("CreateRouteTable returned no id: %s", resp.Body.String())
	}

	return rtID
}

// A caller tearing down a VPC can only learn association ids from
// DescribeRouteTables. If Associate succeeds but Describe omits the
// associationSet, teardown silently skips disassociation and reports success
// over a VPC it never actually drained — so the round-trip, not the individual
// call, is what this test pins.
func TestRouteTableAssociationRoundTrip(t *testing.T) {
	h := newFullHandler()
	vpcID, subnetID := mkVPCAndSubnet(t, h)
	rtID := mkRouteTable(t, h, vpcID)

	assocResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AssociateRouteTable"}, "RouteTableId": {rtID}, "SubnetId": {subnetID},
	})
	if assocResp.Code != http.StatusOK {
		t.Fatalf("AssociateRouteTable = %d: %s", assocResp.Code, assocResp.Body.String())
	}

	assocID := between(assocResp.Body.String(), "<associationId>", "</associationId>")
	if assocID == "" {
		t.Fatalf("AssociateRouteTable returned no associationId: %s", assocResp.Body.String())
	}

	descResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeRouteTables"}, "RouteTableId.1": {rtID},
	})
	if descResp.Code != http.StatusOK {
		t.Fatalf("DescribeRouteTables = %d: %s", descResp.Code, descResp.Body.String())
	}

	body := descResp.Body.String()

	// The exact element name matters: the AWS SDK unmarshals
	// routeTableAssociationId into Associations[].RouteTableAssociationId, and
	// that is the field the teardown path reads.
	gotAssoc := between(body, "<routeTableAssociationId>", "</routeTableAssociationId>")
	if gotAssoc != assocID {
		t.Errorf("describe associationId = %q, want %q\nbody: %s", gotAssoc, assocID, body)
	}

	if got := between(body, "<subnetId>", "</subnetId>"); got != subnetID {
		t.Errorf("describe subnetId = %q, want %q", got, subnetID)
	}

	// A subnet association is never the main association; teardown skips
	// main ones, so mislabelling this would strand the association.
	if !strings.Contains(body, "<main>false</main>") {
		t.Errorf("association should be main=false\nbody: %s", body)
	}

	disResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DisassociateRouteTable"}, "AssociationId": {assocID},
	})
	if disResp.Code != http.StatusOK {
		t.Fatalf("DisassociateRouteTable = %d: %s", disResp.Code, disResp.Body.String())
	}

	afterResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeRouteTables"}, "RouteTableId.1": {rtID},
	})
	if strings.Contains(afterResp.Body.String(), assocID) {
		t.Errorf("association %s still present after disassociate: %s", assocID, afterResp.Body.String())
	}
}

// TestDescribeRouteTablesByAssociationFilter guards the IaC waiter path:
// Terraform polls DescribeRouteTables filtered by the association id and expects
// exactly the one owning table, reporting associationState=associated.
func TestDescribeRouteTablesByAssociationFilter(t *testing.T) {
	h := newFullHandler()
	vpcID, subnetID := mkVPCAndSubnet(t, h)
	rtID := mkRouteTable(t, h, vpcID)

	assocResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AssociateRouteTable"}, "RouteTableId": {rtID}, "SubnetId": {subnetID},
	})
	assocID := between(assocResp.Body.String(), "<associationId>", "</associationId>")

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action":        {"DescribeRouteTables"},
		"Filter.1.Name": {"association.route-table-association-id"}, "Filter.1.Value.1": {assocID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("DescribeRouteTables (filtered) = %d: %s", resp.Code, resp.Body.String())
	}

	body := resp.Body.String()
	if got := between(body, "<routeTableId>", "</routeTableId>"); got != rtID {
		t.Errorf("filtered describe returned routeTableId %q, want %q\nbody: %s", got, rtID, body)
	}

	if !strings.Contains(body, "<state>associated</state>") {
		t.Errorf("association should report state=associated\nbody: %s", body)
	}
}

// TestDescribeRouteTablesRejectsUnknownFilter guards against silently returning
// an empty set for a filter we do not model — an empty result could tell a
// caller a route table is gone and let it delete the VPC (DependencyViolation).
func TestDescribeRouteTablesRejectsUnknownFilter(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action":        {"DescribeRouteTables"},
		"Filter.1.Name": {"transit-gateway-id"}, "Filter.1.Value.1": {"tgw-x"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("an unrecognized filter should error, got 200: %s", resp.Body.String())
	}
}

func TestDisassociateUnknownAssociationIsNotFound(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DisassociateRouteTable"}, "AssociationId": {"rtbassoc-does-not-exist"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("disassociating an unknown id should fail, got 200: %s", resp.Body.String())
	}
}

func TestAssociateRouteTableRejectsUnknownSubnet(t *testing.T) {
	h := newFullHandler()
	vpcID, _ := mkVPCAndSubnet(t, h)
	rtID := mkRouteTable(t, h, vpcID)

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AssociateRouteTable"}, "RouteTableId": {rtID}, "SubnetId": {"subnet-nope"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("associating an unknown subnet should fail, got 200: %s", resp.Body.String())
	}
}

func TestDeleteRouteAndRouteTable(t *testing.T) {
	h := newFullHandler()
	vpcID, _ := mkVPCAndSubnet(t, h)
	rtID := mkRouteTable(t, h, vpcID)

	igwResp := do(t, h, http.MethodPost, "/", url.Values{"Action": {"CreateInternetGateway"}})

	igwID := between(igwResp.Body.String(), "<internetGatewayId>", "</internetGatewayId>")
	if igwID == "" {
		t.Fatalf("CreateInternetGateway returned no id: %s", igwResp.Body.String())
	}

	routeResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateRoute"}, "RouteTableId": {rtID},
		"DestinationCidrBlock": {"0.0.0.0/0"}, "GatewayId": {igwID},
	})
	if routeResp.Code != http.StatusOK {
		t.Fatalf("CreateRoute = %d: %s", routeResp.Code, routeResp.Body.String())
	}

	delRoute := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteRoute"}, "RouteTableId": {rtID}, "DestinationCidrBlock": {"0.0.0.0/0"},
	})
	if delRoute.Code != http.StatusOK {
		t.Fatalf("DeleteRoute = %d: %s", delRoute.Code, delRoute.Body.String())
	}

	// Deleting the same route twice must not report success — teardown retries,
	// and a lying second delete would hide a route that never went away.
	again := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteRoute"}, "RouteTableId": {rtID}, "DestinationCidrBlock": {"0.0.0.0/0"},
	})
	if again.Code == http.StatusOK {
		t.Errorf("second DeleteRoute should fail, got 200: %s", again.Body.String())
	}

	delRT := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteRouteTable"}, "RouteTableId": {rtID},
	})
	if delRT.Code != http.StatusOK {
		t.Fatalf("DeleteRouteTable = %d: %s", delRT.Code, delRT.Body.String())
	}

	after := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeRouteTables"}, "RouteTableId.1": {rtID},
	})
	if strings.Contains(after.Body.String(), rtID) {
		t.Errorf("route table %s still present after delete: %s", rtID, after.Body.String())
	}
}

func TestDeleteInternetGateway(t *testing.T) {
	h := newFullHandler()

	igwResp := do(t, h, http.MethodPost, "/", url.Values{"Action": {"CreateInternetGateway"}})

	igwID := between(igwResp.Body.String(), "<internetGatewayId>", "</internetGatewayId>")
	if igwID == "" {
		t.Fatalf("CreateInternetGateway returned no id: %s", igwResp.Body.String())
	}

	del := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteInternetGateway"}, "InternetGatewayId": {igwID},
	})
	if del.Code != http.StatusOK {
		t.Fatalf("DeleteInternetGateway = %d: %s", del.Code, del.Body.String())
	}

	after := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeInternetGateways"}})
	if strings.Contains(after.Body.String(), igwID) {
		t.Errorf("igw %s still present after delete: %s", igwID, after.Body.String())
	}

	again := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteInternetGateway"}, "InternetGatewayId": {igwID},
	})
	if again.Code == http.StatusOK {
		t.Errorf("deleting an absent igw should fail, got 200: %s", again.Body.String())
	}
}

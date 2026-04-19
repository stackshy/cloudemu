package ec2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/config"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	awsec2 "github.com/stackshy/cloudemu/providers/aws/ec2"
	awsvpc "github.com/stackshy/cloudemu/providers/aws/vpc"
)

// newFullHandler wires both compute and VPC drivers for Phase-2 tests.
func newFullHandler() *Handler {
	opts := config.NewOptions()

	return New(awsec2.New(opts), awsvpc.New(opts))
}

func TestRouteVPCDispatchesAllActions(t *testing.T) {
	h := newFullHandler()

	// Build a VPC so IDs exist for later actions.
	createVPC := do(t, h, http.MethodPost, "/", url.Values{
		"Action":    {"CreateVpc"},
		"CidrBlock": {"10.0.0.0/16"},
	})

	vpcID := between(createVPC.Body.String(), "<vpcId>", "</vpcId>")
	if vpcID == "" {
		t.Fatalf("CreateVpc didn't return a vpcId: %s", createVPC.Body.String())
	}

	// Describe, Delete round out the VPC verbs.
	rr := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeVpcs"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeVpcs = %d: %s", rr.Code, rr.Body.String())
	}

	// Subnet.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateSubnet"}, "VpcId": {vpcID}, "CidrBlock": {"10.0.1.0/24"},
	})

	subnetID := between(rr.Body.String(), "<subnetId>", "</subnetId>")
	if subnetID == "" {
		t.Fatalf("CreateSubnet didn't return subnetId: %s", rr.Body.String())
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeSubnets"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeSubnets = %d", rr.Code)
	}

	// Security group.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action":           {"CreateSecurityGroup"},
		"GroupName":        {"web"},
		"GroupDescription": {"desc"},
		"VpcId":            {vpcID},
	})

	sgID := between(rr.Body.String(), "<groupId>", "</groupId>")
	if sgID == "" {
		t.Fatalf("CreateSecurityGroup didn't return groupId: %s", rr.Body.String())
	}

	// Authorize an ingress rule.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action":                       {"AuthorizeSecurityGroupIngress"},
		"GroupId":                      {sgID},
		"IpPermissions.1.IpProtocol":   {"tcp"},
		"IpPermissions.1.FromPort":     {"80"},
		"IpPermissions.1.ToPort":       {"80"},
		"IpPermissions.1.IpRanges.1.CidrIp": {"0.0.0.0/0"},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("AuthorizeSecurityGroupIngress = %d: %s", rr.Code, rr.Body.String())
	}

	// Egress, Revoke variants use the same parser path.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action":                              {"AuthorizeSecurityGroupEgress"},
		"GroupId":                             {sgID},
		"IpPermissions.1.IpProtocol":          {"-1"},
		"IpPermissions.1.IpRanges.1.CidrIp":   {"0.0.0.0/0"},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("AuthorizeSecurityGroupEgress = %d", rr.Code)
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action":                            {"RevokeSecurityGroupIngress"},
		"GroupId":                           {sgID},
		"IpPermissions.1.IpProtocol":        {"tcp"},
		"IpPermissions.1.FromPort":          {"80"},
		"IpPermissions.1.ToPort":            {"80"},
		"IpPermissions.1.IpRanges.1.CidrIp": {"0.0.0.0/0"},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("RevokeSecurityGroupIngress = %d: %s", rr.Code, rr.Body.String())
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeSecurityGroups"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeSecurityGroups = %d", rr.Code)
	}

	// Internet Gateway.
	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"CreateInternetGateway"}})

	igwID := between(rr.Body.String(), "<internetGatewayId>", "</internetGatewayId>")
	if igwID == "" {
		t.Fatalf("CreateInternetGateway didn't return id: %s", rr.Body.String())
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AttachInternetGateway"}, "InternetGatewayId": {igwID}, "VpcId": {vpcID},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("AttachInternetGateway = %d", rr.Code)
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeInternetGateways"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeInternetGateways = %d", rr.Code)
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DetachInternetGateway"}, "InternetGatewayId": {igwID}, "VpcId": {vpcID},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("DetachInternetGateway = %d", rr.Code)
	}

	// Route Table + Route.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateRouteTable"}, "VpcId": {vpcID},
	})

	rtID := between(rr.Body.String(), "<routeTableId>", "</routeTableId>")
	if rtID == "" {
		t.Fatalf("CreateRouteTable didn't return id: %s", rr.Body.String())
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action":               {"CreateRoute"},
		"RouteTableId":         {rtID},
		"DestinationCidrBlock": {"0.0.0.0/0"},
		"GatewayId":            {igwID},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("CreateRoute = %d: %s", rr.Code, rr.Body.String())
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeRouteTables"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeRouteTables = %d", rr.Code)
	}

	// Clean-up path: Delete subnet + VPC.
	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DeleteSubnet"}, "SubnetId": {subnetID}})
	if rr.Code != http.StatusOK {
		t.Errorf("DeleteSubnet = %d", rr.Code)
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteSecurityGroup"}, "GroupId": {sgID},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("DeleteSecurityGroup = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVPCOpsUnknownIDReturnError(t *testing.T) {
	h := newFullHandler()

	cases := []struct {
		name string
		form url.Values
	}{
		{"DeleteVpc", url.Values{"Action": {"DeleteVpc"}, "VpcId": {"vpc-ghost"}}},
		{"DeleteSubnet", url.Values{"Action": {"DeleteSubnet"}, "SubnetId": {"subnet-ghost"}}},
		{"DeleteSecurityGroup", url.Values{"Action": {"DeleteSecurityGroup"}, "GroupId": {"sg-ghost"}}},
		{
			"AttachInternetGateway",
			url.Values{
				"Action": {"AttachInternetGateway"},
				"InternetGatewayId": {"igw-ghost"}, "VpcId": {"vpc-ghost"},
			},
		},
	}

	for _, tc := range cases {
		rr := do(t, h, http.MethodPost, "/", tc.form)
		if rr.Code == http.StatusOK {
			t.Errorf("%s with ghost id should not return 200 (body=%s)", tc.name, rr.Body.String())
		}
	}
}

func TestDeleteSecurityGroupMissingGroupIDReturns400(t *testing.T) {
	h := newFullHandler()

	rr := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DeleteSecurityGroup"}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestAuthorizeSecurityGroupMissingGroupReturns400(t *testing.T) {
	h := newFullHandler()

	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action":                     {"AuthorizeSecurityGroupIngress"},
		"IpPermissions.1.IpProtocol": {"tcp"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing GroupId, got %d", rr.Code)
	}
}

func TestAuthorizeSecurityGroupEmptyRulesReturns400(t *testing.T) {
	h := newFullHandler()

	// Create a real VPC + SG first so GroupId is valid — only rules are missing.
	vpc := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateVpc"}, "CidrBlock": {"10.0.0.0/16"},
	})
	vpcID := between(vpc.Body.String(), "<vpcId>", "</vpcId>")

	create := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateSecurityGroup"},
		"GroupName": {"x"}, "GroupDescription": {"x"}, "VpcId": {vpcID},
	})

	sgID := between(create.Body.String(), "<groupId>", "</groupId>")
	if sgID == "" {
		t.Fatalf("SG creation failed: %s", create.Body.String())
	}

	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AuthorizeSecurityGroupIngress"}, "GroupId": {sgID},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty rules, got %d: %s", rr.Code, rr.Body.String())
	}

	// Also hit revokeSecurityGroupEgress to ensure it's covered.
	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"RevokeSecurityGroupEgress"}, "GroupId": {sgID},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty revoke rules, got %d", rr.Code)
	}
}

func TestCreateRouteMissingTargetReturns400(t *testing.T) {
	h := newFullHandler()

	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateRoute"}, "RouteTableId": {"rtb-x"}, "DestinationCidrBlock": {"0.0.0.0/0"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestParseIPPermissionsNone(t *testing.T) {
	if got := parseIPPermissions(url.Values{}); got != nil {
		t.Errorf("empty form should give nil, got %v", got)
	}
}

func TestParseIPPermissionsSingleCIDR(t *testing.T) {
	form := url.Values{
		"IpPermissions.1.IpProtocol":        {"tcp"},
		"IpPermissions.1.FromPort":          {"22"},
		"IpPermissions.1.ToPort":            {"22"},
		"IpPermissions.1.IpRanges.1.CidrIp": {"10.0.0.0/16"},
	}

	got := parseIPPermissions(form)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1: %+v", len(got), got)
	}

	r := got[0]
	if r.Protocol != "tcp" || r.FromPort != 22 || r.ToPort != 22 || r.CIDR != "10.0.0.0/16" {
		t.Errorf("unexpected rule: %+v", r)
	}
}

func TestParseIPPermissionsMultipleCIDRs(t *testing.T) {
	// One rule with two CIDRs should expand to two SecurityRule entries.
	form := url.Values{
		"IpPermissions.1.IpProtocol":        {"tcp"},
		"IpPermissions.1.FromPort":          {"80"},
		"IpPermissions.1.ToPort":            {"80"},
		"IpPermissions.1.IpRanges.1.CidrIp": {"10.0.0.0/16"},
		"IpPermissions.1.IpRanges.2.CidrIp": {"192.168.0.0/16"},
	}

	got := parseIPPermissions(form)
	if len(got) != 2 {
		t.Fatalf("expected 2 flattened rules, got %d: %+v", len(got), got)
	}
}

func TestParseIPPermissionsWithoutCIDRStillEmits(t *testing.T) {
	// A permission with no CIDR still produces a rule (empty CIDR).
	form := url.Values{
		"IpPermissions.1.IpProtocol": {"icmp"},
	}

	got := parseIPPermissions(form)
	if len(got) != 1 {
		t.Fatalf("expected 1 rule with empty CIDR, got %v", got)
	}

	if got[0].Protocol != "icmp" || got[0].CIDR != "" {
		t.Errorf("unexpected: %+v", got[0])
	}
}

func TestResolveRouteTarget(t *testing.T) {
	cases := []struct {
		form             url.Values
		wantID, wantType string
	}{
		{url.Values{"GatewayId": {"igw-x"}}, "igw-x", "gateway"},
		{url.Values{"NatGatewayId": {"nat-x"}}, "nat-x", "nat-gateway"},
		{url.Values{"VpcPeeringConnectionId": {"pcx-x"}}, "pcx-x", "peering"},
		{url.Values{}, "", ""},
	}

	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodPost, "/", nil) //nolint:noctx // test helper
		req.Form = tc.form

		id, tp := resolveRouteTarget(req)
		if id != tc.wantID || tp != tc.wantType {
			t.Errorf("resolveRouteTarget(%v) = (%q,%q), want (%q,%q)",
				tc.form, id, tp, tc.wantID, tc.wantType)
		}
	}
}

func TestToVpcXMLStateDefaults(t *testing.T) {
	in := &netdriver.VPCInfo{ID: "vpc-x", CIDRBlock: "10.0.0.0/16"} // no State

	got := toVpcXML(in)
	if got.State != "available" {
		t.Errorf("default state should be 'available', got %q", got.State)
	}

	in.State = "pending"
	got = toVpcXML(in)

	if got.State != "pending" {
		t.Errorf("explicit state preserved: %q", got.State)
	}
}

func TestToSubnetXMLStateDefaults(t *testing.T) {
	in := &netdriver.SubnetInfo{ID: "subnet-x", VPCID: "vpc-x"}

	got := toSubnetXML(in)
	if got.State != "available" {
		t.Errorf("default state: %q", got.State)
	}

	if got.AvailableIPCount != subnetAvailableIPs {
		t.Errorf("AvailableIPCount = %d, want %d",
			got.AvailableIPCount, subnetAvailableIPs)
	}
}

func TestToInternetGatewayXMLAttachment(t *testing.T) {
	detached := &netdriver.InternetGateway{ID: "igw-1"}
	if got := toInternetGatewayXML(detached); len(got.Attachments) != 0 {
		t.Errorf("detached IGW should have 0 attachments, got %d", len(got.Attachments))
	}

	attached := &netdriver.InternetGateway{ID: "igw-2", VpcID: "vpc-1"}
	got := toInternetGatewayXML(attached)

	if len(got.Attachments) != 1 {
		t.Fatalf("attached IGW should have 1 attachment, got %d", len(got.Attachments))
	}

	if got.Attachments[0].VpcID != "vpc-1" || got.Attachments[0].State != "attached" {
		t.Errorf("attachment wrong: %+v", got.Attachments[0])
	}
}

func TestToRouteTableXMLTargetMapping(t *testing.T) {
	rt := &netdriver.RouteTable{
		ID: "rtb-1", VPCID: "vpc-1",
		Routes: []netdriver.Route{
			{DestinationCIDR: "0.0.0.0/0", TargetID: "igw-1", TargetType: "gateway", State: "active"},
			{DestinationCIDR: "10.1.0.0/16", TargetID: "nat-1", TargetType: "nat-gateway"},
			{DestinationCIDR: "172.16.0.0/12", TargetID: "pcx-1", TargetType: "peering"},
		},
	}

	got := toRouteTableXML(rt)
	if len(got.Routes) != 3 {
		t.Fatalf("len=%d want 3", len(got.Routes))
	}

	if got.Routes[0].GatewayID != "igw-1" {
		t.Errorf("gateway target: %+v", got.Routes[0])
	}

	if got.Routes[1].NatGatewayID != "nat-1" {
		t.Errorf("nat target: %+v", got.Routes[1])
	}

	if got.Routes[2].VpcPeeringConnection != "pcx-1" {
		t.Errorf("peering target: %+v", got.Routes[2])
	}
}

func TestToTagItemsSorted(t *testing.T) {
	in := map[string]string{"Z": "last", "A": "first", "M": "middle"}

	got := toTagItems(in)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}

	if got[0].Key != "A" || got[1].Key != "M" || got[2].Key != "Z" {
		t.Errorf("tags not sorted by key: %+v", got)
	}
}

func TestToTagItemsEmpty(t *testing.T) {
	if got := toTagItems(nil); got != nil {
		t.Errorf("nil input should give nil, got %v", got)
	}
}

func TestNonEmpty(t *testing.T) {
	if got := nonEmpty("x", "fallback"); got != "x" {
		t.Errorf("got %q", got)
	}

	if got := nonEmpty("", "fallback"); got != "fallback" {
		t.Errorf("got %q", got)
	}
}

func TestToIPPermissionXMLsGroupsByTuple(t *testing.T) {
	// Two rules with the same protocol/ports + different CIDRs should
	// group into ONE ipPermission XML element with two ipRanges.
	rules := []netdriver.SecurityRule{
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "10.0.0.0/16"},
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "192.168.0.0/16"},
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"},
	}

	got := toIPPermissionXMLs(rules)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 tuples", len(got))
	}

	// First tuple (80/tcp) should have both CIDRs bundled.
	if len(got[0].IPRanges) != 2 {
		t.Errorf("expected 2 ipRanges for tuple 0, got %d: %+v", len(got[0].IPRanges), got[0])
	}
}

func TestToIPPermissionXMLsEmpty(t *testing.T) {
	if got := toIPPermissionXMLs(nil); got != nil {
		t.Errorf("empty rules should give nil, got %v", got)
	}
}

// between returns the substring between open and close markers, or empty.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}

	j := strings.Index(s[i+len(open):], close)
	if j < 0 {
		return ""
	}

	return s[i+len(open) : i+len(open)+j]
}

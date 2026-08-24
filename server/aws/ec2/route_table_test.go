package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// TestRouteTableOwnerID pins that a route table reports its owning account id.
// Terraform's aws_route_table and aws_default_route_table read ownerId; an empty
// value makes the resource look cross-account and breaks import/refresh.
func TestRouteTableOwnerID(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: vpc.Vpc.VpcId})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	if got := aws.ToString(rt.RouteTable.OwnerId); got != "123456789012" {
		t.Errorf("CreateRouteTable OwnerId = %q, want 123456789012", got)
	}

	desc, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		RouteTableIds: []string{aws.ToString(rt.RouteTable.RouteTableId)},
	})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	if got := aws.ToString(desc.RouteTables[0].OwnerId); got != "123456789012" {
		t.Errorf("DescribeRouteTables OwnerId = %q, want 123456789012", got)
	}
}

// TestReplaceRoute pins ReplaceRoute: it swaps the target of an existing route
// keyed by destination CIDR, and returns InvalidRoute.NotFound when the route
// does not already exist (its documented precondition).
func TestReplaceRoute(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpc.Vpc.VpcId)

	igw1 := mkIGW(ctx, t, c)
	igw2 := mkIGW(ctx, t, c)

	rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	rtID := aws.ToString(rt.RouteTable.RouteTableId)

	if _, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igw1),
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	if _, err := c.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igw2),
	}); err != nil {
		t.Fatalf("ReplaceRoute: %v", err)
	}

	desc, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{RouteTableIds: []string{rtID}})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	route := findRouteByCIDR(desc.RouteTables[0].Routes, "0.0.0.0/0")
	if route == nil {
		t.Fatalf("route 0.0.0.0/0 not found after replace; routes: %+v", desc.RouteTables[0].Routes)
	}

	if got := aws.ToString(route.GatewayId); got != igw2 {
		t.Errorf("route gatewayId = %q, want %q (replaced target)", got, igw2)
	}

	_, err = c.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("192.168.0.0/16"),
		GatewayId:            aws.String(igw2),
	})
	if err == nil {
		t.Fatal("ReplaceRoute on a non-existent route should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidRoute.NotFound" {
		t.Fatalf("want InvalidRoute.NotFound, got %v", err)
	}

	// A missing route TABLE is a different error code than a missing route.
	_, err = c.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String("rtb-doesnotexist"),
		DestinationCidrBlock: aws.String("10.0.0.0/16"),
		GatewayId:            aws.String(igw2),
	})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidRouteTableID.NotFound" {
		t.Fatalf("want InvalidRouteTableID.NotFound for a bogus route table, got %v", err)
	}
}

// mkIGW creates an internet gateway and returns its id.
func mkIGW(ctx context.Context, t *testing.T, c *ec2.Client) string {
	t.Helper()

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	return aws.ToString(igw.InternetGateway.InternetGatewayId)
}

// findRouteByCIDR returns the route with the given destination CIDR, or nil.
func findRouteByCIDR(routes []ec2types.Route, cidr string) *ec2types.Route {
	for i := range routes {
		if aws.ToString(routes[i].DestinationCidrBlock) == cidr {
			return &routes[i]
		}
	}

	return nil
}

// TestRouteTableLocalRoute pins that a new route table's implicit VPC-local
// route reports gatewayId "local" and origin CreateRouteTable. Terraform's
// aws_route_table and aws_default_route_table read these to distinguish the
// local route from managed routes; an empty gatewayId makes the local route
// look like an unmanaged one they then try to delete.
func TestRouteTableLocalRoute(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpc.Vpc.VpcId)

	rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	rtID := aws.ToString(rt.RouteTable.RouteTableId)

	desc, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		RouteTableIds: []string{rtID},
	})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	if len(desc.RouteTables) != 1 {
		t.Fatalf("DescribeRouteTables = %d, want 1", len(desc.RouteTables))
	}

	local := findLocalRoute(desc.RouteTables[0].Routes)
	if local == nil {
		t.Fatalf("no local route found; routes: %+v", desc.RouteTables[0].Routes)
	}

	if got := aws.ToString(local.GatewayId); got != "local" {
		t.Errorf("local route gatewayId = %q, want local", got)
	}

	if local.Origin != ec2types.RouteOriginCreateRouteTable {
		t.Errorf("local route origin = %q, want CreateRouteTable", local.Origin)
	}

	if got := aws.ToString(local.DestinationCidrBlock); got != "10.0.0.0/16" {
		t.Errorf("local route destination = %q, want 10.0.0.0/16", got)
	}
}

func findLocalRoute(routes []ec2types.Route) *ec2types.Route {
	for i := range routes {
		if aws.ToString(routes[i].GatewayId) == "local" {
			return &routes[i]
		}
	}

	return nil
}

// TestDescribeRouteTablesPaginatesAllOnce pins that DescribeRouteTables honors
// MaxResults/NextToken, paging every table (the VPC's main table plus the two
// created here) exactly once with no duplicates across pages.
func TestDescribeRouteTablesPaginatesAllOnce(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

	created := map[string]bool{}
	for range 2 {
		rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
		if err != nil {
			t.Fatalf("CreateRouteTable: %v", err)
		}

		created[aws.ToString(rt.RouteTable.RouteTableId)] = true
	}

	seen := map[string]int{}
	pages := 0

	var token *string

	for {
		out, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
			MaxResults: aws.Int32(1),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeRouteTables: %v", err)
		}

		if len(out.RouteTables) > 1 {
			t.Fatalf("page returned %d route tables, want at most 1", len(out.RouteTables))
		}

		for _, rt := range out.RouteTables {
			seen[aws.ToString(rt.RouteTableId)]++
		}

		pages++

		if aws.ToString(out.NextToken) == "" {
			break
		}

		token = out.NextToken
	}

	if pages < 3 {
		t.Fatalf("paged in %d pages, want >=3 (one table per page)", pages)
	}

	for id := range created {
		if seen[id] != 1 {
			t.Fatalf("created route table %s seen %d times, want exactly 1", id, seen[id])
		}
	}

	for id, n := range seen {
		if n != 1 {
			t.Fatalf("route table %s seen %d times across pages, want 1", id, n)
		}
	}
}

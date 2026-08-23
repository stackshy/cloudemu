package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

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

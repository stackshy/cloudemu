package ec2_test

import (
	"context"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func mkGatewayEndpoint(t *testing.T, c *ec2.Client, vpcID string, rts ...string) (string, error) {
	t.Helper()

	ep, err := c.CreateVpcEndpoint(context.Background(), &ec2.CreateVpcEndpointInput{
		VpcId: aws.String(vpcID), ServiceName: aws.String(s3PrefixListName),
		VpcEndpointType: ec2types.VpcEndpointTypeGateway, RouteTableIds: rts,
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(ep.VpcEndpoint.VpcEndpointId), nil
}

// TestModifyVpcEndpointConcurrentAddRouteTable pins that parallel
// AddRouteTableId calls on one endpoint all land. Terraform creates several
// aws_vpc_endpoint_route_table_association resources at once, and a lost update
// here left tables (and their pl- routes) missing.
func TestModifyVpcEndpointConcurrentAddRouteTable(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)

	rts := make([]string, 0, 6)
	for range 6 {
		rts = append(rts, mkRouteTable(t, c, vpcID))
	}

	vpceID, err := mkGatewayEndpoint(t, c, vpcID)
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	var wg sync.WaitGroup

	for _, rt := range rts {
		wg.Add(1)

		go func(rt string) {
			defer wg.Done()

			if _, err := c.ModifyVpcEndpoint(ctx, &ec2.ModifyVpcEndpointInput{
				VpcEndpointId: aws.String(vpceID), AddRouteTableIds: []string{rt},
			}); err != nil {
				t.Errorf("ModifyVpcEndpoint add %s: %v", rt, err)
			}
		}(rt)
	}

	wg.Wait()

	out, err := c.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{VpcEndpointIds: []string{vpceID}})
	if err != nil {
		t.Fatalf("DescribeVpcEndpoints: %v", err)
	}

	if got := len(out.VpcEndpoints[0].RouteTableIds); got != len(rts) {
		t.Fatalf("endpoint has %d route tables, want %d", got, len(rts))
	}

	for _, rt := range rts {
		if findVPCERoute(t, c, rt, vpceID) == nil {
			t.Errorf("route table %s has no route to %s", rt, vpceID)
		}
	}
}

// TestGatewayEndpointDuplicateServiceRouteRejected pins the EC2 rule that a
// route table holds one endpoint route per service: a second s3 Gateway
// endpoint on the same table fails with RouteAlreadyExists.
func TestGatewayEndpointDuplicateServiceRouteRejected(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	rtA := mkRouteTable(t, c, vpcID)
	rtB := mkRouteTable(t, c, vpcID)

	if _, err := mkGatewayEndpoint(t, c, vpcID, rtA); err != nil {
		t.Fatalf("first endpoint: %v", err)
	}

	_, err := mkGatewayEndpoint(t, c, vpcID, rtA)
	requireAPIErrorCode(t, err, "RouteAlreadyExists")

	second, err := mkGatewayEndpoint(t, c, vpcID, rtB)
	if err != nil {
		t.Fatalf("s3 endpoint on another table: %v", err)
	}

	_, err = c.ModifyVpcEndpoint(ctx, &ec2.ModifyVpcEndpointInput{
		VpcEndpointId: aws.String(second), AddRouteTableIds: []string{rtA},
	})
	requireAPIErrorCode(t, err, "RouteAlreadyExists")
}

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

// TestVPCEndpointGatewayLifecycle exercises the consumer gateway-endpoint flow
// Terraform's aws_vpc_endpoint drives: create against a VPC, read it back, and
// delete it.
func TestVPCEndpointGatewayLifecycle(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	create, err := client.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		VpcId:           aws.String(vpcID),
		ServiceName:     aws.String("com.amazonaws.us-east-1.s3"),
		VpcEndpointType: ec2types.VpcEndpointTypeGateway,
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	ep := create.VpcEndpoint
	epID := aws.ToString(ep.VpcEndpointId)

	if epID == "" {
		t.Fatal("CreateVpcEndpoint returned empty endpoint id")
	}
	if ep.VpcEndpointType != ec2types.VpcEndpointTypeGateway {
		t.Errorf("VpcEndpointType = %q, want Gateway", ep.VpcEndpointType)
	}
	if aws.ToString(ep.VpcId) != vpcID {
		t.Errorf("VpcId = %q, want %q", aws.ToString(ep.VpcId), vpcID)
	}
	if string(ep.State) != "available" {
		t.Errorf("State = %q, want available", ep.State)
	}
	// A Gateway endpoint is a route-table entry and provisions no ENIs.
	if len(ep.NetworkInterfaceIds) != 0 {
		t.Errorf("Gateway endpoint NetworkInterfaceIds = %v, want none", ep.NetworkInterfaceIds)
	}

	desc, err := client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		VpcEndpointIds: []string{epID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcEndpoints: %v", err)
	}
	if len(desc.VpcEndpoints) != 1 || aws.ToString(desc.VpcEndpoints[0].VpcEndpointId) != epID {
		t.Fatalf("DescribeVpcEndpoints = %+v, want one endpoint %q", desc.VpcEndpoints, epID)
	}

	if _, err := client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
		VpcEndpointIds: []string{epID},
	}); err != nil {
		t.Fatalf("DeleteVpcEndpoints: %v", err)
	}
}

// TestVPCEndpointInterfaceCarriesSubnetsAndGroups pins that an interface
// endpoint (SSM/ECR style) retains the subnets and security groups it was
// created with — Terraform reads these back to avoid drift.
func TestVPCEndpointInterfaceCarriesSubnetsAndGroups(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	subnet, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subnetID := aws.ToString(subnet.Subnet.SubnetId)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("ep-sg"),
		Description: aws.String("endpoint sg"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := aws.ToString(sg.GroupId)

	create, err := client.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		VpcId:            aws.String(vpcID),
		ServiceName:      aws.String("com.amazonaws.us-east-1.ssm"),
		VpcEndpointType:  ec2types.VpcEndpointTypeInterface,
		SubnetIds:        []string{subnetID},
		SecurityGroupIds: []string{sgID},
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	ep := create.VpcEndpoint
	if len(ep.SubnetIds) != 1 || ep.SubnetIds[0] != subnetID {
		t.Errorf("SubnetIds = %v, want [%s]", ep.SubnetIds, subnetID)
	}

	// An Interface endpoint provisions one backing ENI per subnet; Terraform's
	// aws_vpc_endpoint reads network_interface_ids off the response.
	if len(ep.NetworkInterfaceIds) != 1 {
		t.Fatalf("NetworkInterfaceIds = %v, want one per subnet", ep.NetworkInterfaceIds)
	}

	// The ENI is a real interface DescribeNetworkInterfaces can see.
	enis, err := client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{ep.NetworkInterfaceIds[0]},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}
	if len(enis.NetworkInterfaces) != 1 {
		t.Errorf("endpoint ENI %q not found via DescribeNetworkInterfaces", ep.NetworkInterfaceIds[0])
	}
}

// TestDeleteVpcEndpointsIsIdempotent pins that deleting an unknown endpoint id
// returns HTTP 200 with the id in the Unsuccessful set (not a top-level error),
// so a re-run of a destroy over an already-gone endpoint still succeeds.
func TestDeleteVpcEndpointsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	out, err := client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
		VpcEndpointIds: []string{"vpce-00000000000000000"},
	})
	if err != nil {
		t.Fatalf("DeleteVpcEndpoints(unknown) returned top-level error: %v", err)
	}
	if len(out.Unsuccessful) != 1 {
		t.Fatalf("Unsuccessful = %d, want 1", len(out.Unsuccessful))
	}
	if code := aws.ToString(out.Unsuccessful[0].Error.Code); code != "InvalidVpcEndpointId.NotFound" {
		t.Errorf("Unsuccessful error code = %q, want InvalidVpcEndpointId.NotFound", code)
	}
}

// TestDescribeVpcEndpointsUnknownIDNotFound pins that an explicit, well-formed
// but non-existent endpoint id is InvalidVpcEndpointId.NotFound.
func TestDescribeVpcEndpointsUnknownIDNotFound(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		VpcEndpointIds: []string{"vpce-00000000000000000"},
	})
	if err == nil {
		t.Fatal("DescribeVpcEndpoints(unknown) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidVpcEndpointId.NotFound" {
		t.Fatalf("error = %v, want InvalidVpcEndpointId.NotFound", err)
	}
}

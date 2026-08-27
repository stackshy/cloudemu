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

// TestCreateTagsOnRouteTable pins that CreateTags/DeleteTags reach VPC-family
// resources that have no dedicated Update*Tags method (route tables here). The
// tag must be visible on DescribeRouteTables and gone after DeleteTags —
// previously these id prefixes fell through to the compute tagger and the tag
// was silently dropped.
func TestCreateTagsOnRouteTable(t *testing.T) {
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

	rtID := aws.ToString(rt.RouteTable.RouteTableId)

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{rtID},
		Tags:      []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("public")}},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	if got := routeTableTagValue(ctx, t, c, rtID, "Name"); got != "public" {
		t.Fatalf("route table tag Name = %q, want public", got)
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{rtID},
		Tags:      []ec2types.Tag{{Key: aws.String("Name")}},
	}); err != nil {
		t.Fatalf("DeleteTags: %v", err)
	}

	if got := routeTableTagValue(ctx, t, c, rtID, "Name"); got != "" {
		t.Fatalf("route table tag Name = %q after delete, want empty", got)
	}
}

// TestCreateTagsOnDhcpOptions pins tagging on a second store (DHCP option sets)
// so the id-prefix routing is exercised beyond route tables.
func TestCreateTagsOnDhcpOptions(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	opts, err := c.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
		DhcpConfigurations: []ec2types.NewDhcpConfiguration{{
			Key:    aws.String("domain-name-servers"),
			Values: []string{"10.0.0.2"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateDhcpOptions: %v", err)
	}

	doptID := aws.ToString(opts.DhcpOptions.DhcpOptionsId)

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{doptID},
		Tags:      []ec2types.Tag{{Key: aws.String("Env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	desc, err := c.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{DhcpOptionsIds: []string{doptID}})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions: %v", err)
	}

	if got := tagValue(desc.DhcpOptions[0].Tags, "Env"); got != "prod" {
		t.Fatalf("dhcp options tag Env = %q, want prod", got)
	}
}

// TestCreateTagsUnknownRouteTableErrorCode pins that CreateTags on a non-existent
// VPC-family id returns InvalidID.NotFound rather than silently succeeding.
func TestCreateTagsUnknownRouteTableErrorCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{"rtb-doesnotexist0"},
		Tags:      []ec2types.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	if err == nil {
		t.Fatal("CreateTags on unknown route table id should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidID.NotFound" {
		t.Fatalf("want InvalidID.NotFound, got %v", err)
	}
}

func routeTableTagValue(ctx context.Context, t *testing.T, c *ec2.Client, rtID, key string) string {
	t.Helper()

	desc, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{RouteTableIds: []string{rtID}})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	return tagValue(desc.RouteTables[0].Tags, key)
}

func tagValue(tags []ec2types.Tag, key string) string {
	for i := range tags {
		if aws.ToString(tags[i].Key) == key {
			return aws.ToString(tags[i].Value)
		}
	}

	return ""
}

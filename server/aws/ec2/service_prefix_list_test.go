package ec2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

const (
	s3PrefixListName     = "com.amazonaws.us-east-1.s3"
	dynamoPrefixListName = "com.amazonaws.us-east-1.dynamodb"
)

// servicePrefixListIDs maps each AWS service prefix list name to its pl- id.
func servicePrefixListIDs(t *testing.T, c *ec2.Client) map[string]string {
	t.Helper()

	out, err := c.DescribePrefixLists(context.Background(), &ec2.DescribePrefixListsInput{})
	if err != nil {
		t.Fatalf("DescribePrefixLists: %v", err)
	}

	ids := map[string]string{}

	for _, pl := range out.PrefixLists {
		if len(pl.Cidrs) == 0 {
			t.Errorf("prefix list %s has no cidrs", aws.ToString(pl.PrefixListName))
		}

		ids[aws.ToString(pl.PrefixListName)] = aws.ToString(pl.PrefixListId)
	}

	return ids
}

func requireAPIErrorCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want API error %s, got %v", want, err)
	}

	if apiErr.ErrorCode() != want {
		t.Fatalf("error code = %s, want %s", apiErr.ErrorCode(), want)
	}
}

// TestDescribePrefixListsServiceLists pins the unfiltered answer: the s3 and
// dynamodb lists for the caller's region, with stable pl- ids.
func TestDescribePrefixListsServiceLists(t *testing.T) {
	c := newRoutingEdgeEC2(t)

	ids := servicePrefixListIDs(t, c)
	if len(ids) != 2 {
		t.Fatalf("got %d prefix lists, want 2: %v", len(ids), ids)
	}

	for _, name := range []string{s3PrefixListName, dynamoPrefixListName} {
		if !strings.HasPrefix(ids[name], "pl-") {
			t.Errorf("%s id = %q, want a pl- id", name, ids[name])
		}
	}

	again := servicePrefixListIDs(t, c)
	if again[s3PrefixListName] != ids[s3PrefixListName] {
		t.Errorf("s3 prefix list id changed between calls: %s then %s", ids[s3PrefixListName], again[s3PrefixListName])
	}
}

// TestDescribePrefixListsFilters covers the prefix-list-name and
// prefix-list-id filters, PrefixListIds, and MaxResults/NextToken paging.
// Terraform's aws_vpc_endpoint read looks the list up by prefix-list-name.
func TestDescribePrefixListsFilters(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	ids := servicePrefixListIDs(t, c)

	byName, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{
		Filters: []ec2types.Filter{{Name: aws.String("prefix-list-name"), Values: []string{s3PrefixListName}}},
	})
	if err != nil {
		t.Fatalf("filter by name: %v", err)
	}

	if len(byName.PrefixLists) != 1 || aws.ToString(byName.PrefixLists[0].PrefixListId) != ids[s3PrefixListName] {
		t.Fatalf("filter by name = %+v", byName.PrefixLists)
	}

	byIDFilter, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{
		Filters: []ec2types.Filter{{Name: aws.String("prefix-list-id"), Values: []string{ids[dynamoPrefixListName]}}},
	})
	if err != nil {
		t.Fatalf("filter by id: %v", err)
	}

	if len(byIDFilter.PrefixLists) != 1 || aws.ToString(byIDFilter.PrefixLists[0].PrefixListName) != dynamoPrefixListName {
		t.Fatalf("filter by id = %+v", byIDFilter.PrefixLists)
	}

	byIDs, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{PrefixListIds: []string{ids[s3PrefixListName]}})
	if err != nil {
		t.Fatalf("PrefixListIds: %v", err)
	}

	if len(byIDs.PrefixLists) != 1 {
		t.Fatalf("PrefixListIds returned %d lists, want 1", len(byIDs.PrefixLists))
	}

	none, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{
		Filters: []ec2types.Filter{{Name: aws.String("prefix-list-name"), Values: []string{"com.amazonaws.us-east-1.ssm"}}},
	})
	if err != nil || len(none.PrefixLists) != 0 {
		t.Fatalf("filter by non-gateway service = %+v, %v; want empty", none, err)
	}

	first, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{MaxResults: aws.Int32(1)})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	if len(first.PrefixLists) != 1 || first.NextToken == nil {
		t.Fatalf("page 1 = %d lists, next %v", len(first.PrefixLists), first.NextToken)
	}

	second, err := c.DescribePrefixLists(ctx, &ec2.DescribePrefixListsInput{MaxResults: aws.Int32(1), NextToken: first.NextToken})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if len(second.PrefixLists) != 1 || second.NextToken != nil ||
		aws.ToString(second.PrefixLists[0].PrefixListId) == aws.ToString(first.PrefixLists[0].PrefixListId) {
		t.Fatalf("page 2 = %+v next %v", second.PrefixLists, second.NextToken)
	}
}

// TestDescribePrefixListsUnknownID pins the EC2 error code for an unknown id.
func TestDescribePrefixListsUnknownID(t *testing.T) {
	c := newRoutingEdgeEC2(t)

	_, err := c.DescribePrefixLists(context.Background(), &ec2.DescribePrefixListsInput{
		PrefixListIds: []string{"pl-00000000"},
	})
	requireAPIErrorCode(t, err, "InvalidPrefixListID.NotFound")
}

// TestGatewayEndpointPrefixListRoute pins that a Gateway s3 endpoint writes a
// route with destinationPrefixListId (the s3 pl- id) and gatewayId (the
// vpce- id) into its route table, and that deleting the endpoint removes it.
func TestGatewayEndpointPrefixListRoute(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	ids := servicePrefixListIDs(t, c)

	rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	rtID := aws.ToString(rt.RouteTable.RouteTableId)

	ep, err := c.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		VpcId: aws.String(vpcID), ServiceName: aws.String(s3PrefixListName),
		VpcEndpointType: ec2types.VpcEndpointTypeGateway, RouteTableIds: []string{rtID},
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	vpceID := aws.ToString(ep.VpcEndpoint.VpcEndpointId)

	route := findVPCERoute(t, c, rtID, vpceID)
	if route == nil {
		t.Fatal("no route to the gateway endpoint in the route table")
	}

	if got := aws.ToString(route.DestinationPrefixListId); got != ids[s3PrefixListName] {
		t.Errorf("destinationPrefixListId = %q, want %q", got, ids[s3PrefixListName])
	}

	if route.DestinationCidrBlock != nil {
		t.Errorf("destinationCidrBlock = %q, want unset", aws.ToString(route.DestinationCidrBlock))
	}

	if route.State != ec2types.RouteStateActive {
		t.Errorf("route state = %s, want active", route.State)
	}

	if _, err := c.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{VpcEndpointIds: []string{vpceID}}); err != nil {
		t.Fatalf("DeleteVpcEndpoints: %v", err)
	}

	if findVPCERoute(t, c, rtID, vpceID) != nil {
		t.Error("route to the deleted endpoint is still in the route table")
	}
}

func findVPCERoute(t *testing.T, c *ec2.Client, rtID, vpceID string) *ec2types.Route {
	t.Helper()

	out, err := c.DescribeRouteTables(context.Background(), &ec2.DescribeRouteTablesInput{RouteTableIds: []string{rtID}})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	for i := range out.RouteTables[0].Routes {
		r := &out.RouteTables[0].Routes[i]
		if aws.ToString(r.GatewayId) == vpceID {
			return r
		}
	}

	return nil
}

// TestManagedPrefixListsIncludeAWSOwned pins that DescribeManagedPrefixLists
// also lists the AWS-owned service lists (ownerId AWS) and that
// GetManagedPrefixListEntries reads their cidrs.
func TestManagedPrefixListsIncludeAWSOwned(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	ids := servicePrefixListIDs(t, c)

	out, err := c.DescribeManagedPrefixLists(ctx, &ec2.DescribeManagedPrefixListsInput{
		Filters: []ec2types.Filter{{Name: aws.String("prefix-list-name"), Values: []string{s3PrefixListName}}},
	})
	if err != nil {
		t.Fatalf("DescribeManagedPrefixLists: %v", err)
	}

	if len(out.PrefixLists) != 1 {
		t.Fatalf("got %d lists, want 1", len(out.PrefixLists))
	}

	pl := out.PrefixLists[0]
	if aws.ToString(pl.PrefixListId) != ids[s3PrefixListName] || aws.ToString(pl.OwnerId) != "AWS" {
		t.Errorf("list = id %s owner %s", aws.ToString(pl.PrefixListId), aws.ToString(pl.OwnerId))
	}

	if want := "arn:aws:ec2:us-east-1:aws:prefix-list/" + ids[s3PrefixListName]; aws.ToString(pl.PrefixListArn) != want {
		t.Errorf("arn = %s, want %s", aws.ToString(pl.PrefixListArn), want)
	}

	entries, err := c.GetManagedPrefixListEntries(ctx, &ec2.GetManagedPrefixListEntriesInput{PrefixListId: pl.PrefixListId})
	if err != nil {
		t.Fatalf("GetManagedPrefixListEntries: %v", err)
	}

	if len(entries.Entries) == 0 {
		t.Error("AWS-owned list has no entries")
	}

	if pl.MaxEntries != nil || pl.Version != nil {
		t.Errorf("AWS-owned list carries maxEntries %v / version %v, want neither", pl.MaxEntries, pl.Version)
	}

	page1, err := c.GetManagedPrefixListEntries(ctx, &ec2.GetManagedPrefixListEntriesInput{
		PrefixListId: pl.PrefixListId, MaxResults: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("GetManagedPrefixListEntries page 1: %v", err)
	}

	if len(page1.Entries) != 5 || page1.NextToken == nil {
		t.Fatalf("page 1 = %d entries, next %v; want 5 and a token", len(page1.Entries), page1.NextToken)
	}

	page2, err := c.GetManagedPrefixListEntries(ctx, &ec2.GetManagedPrefixListEntriesInput{
		PrefixListId: pl.PrefixListId, MaxResults: aws.Int32(5), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("GetManagedPrefixListEntries page 2: %v", err)
	}

	if len(page1.Entries)+len(page2.Entries) != len(entries.Entries) || page2.NextToken != nil {
		t.Fatalf("page 2 = %d entries, next %v", len(page2.Entries), page2.NextToken)
	}

	_, err = c.DescribeManagedPrefixLists(ctx, &ec2.DescribeManagedPrefixListsInput{PrefixListIds: []string{"pl-00000000"}})
	requireAPIErrorCode(t, err, "InvalidPrefixListID.NotFound")
}

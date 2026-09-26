package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// tagOnlyFilters are the two ways a caller finds its own resource by tag.
func tagOnlyFilters(key, value string) [][]ec2types.Filter {
	return [][]ec2types.Filter{
		{{Name: aws.String("tag:" + key), Values: []string{value}}},
		{{Name: aws.String("tag-key"), Values: []string{key}}},
	}
}

func addressTagged(ctx context.Context, t *testing.T, c *ec2.Client, id string, fs []ec2types.Filter) (map[string]string, bool) {
	t.Helper()

	out, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{Filters: fs})
	if err != nil {
		t.Fatalf("DescribeAddresses: %v", err)
	}

	for i := range out.Addresses {
		if aws.ToString(out.Addresses[i].AllocationId) == id {
			return sdkTagMap(out.Addresses[i].Tags), true
		}
	}

	return nil, false
}

func endpointTagged(ctx context.Context, t *testing.T, c *ec2.Client, id string, fs []ec2types.Filter) (map[string]string, bool) {
	t.Helper()

	out, err := c.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{Filters: fs})
	if err != nil {
		t.Fatalf("DescribeVpcEndpoints: %v", err)
	}

	for i := range out.VpcEndpoints {
		if aws.ToString(out.VpcEndpoints[i].VpcEndpointId) == id {
			return sdkTagMap(out.VpcEndpoints[i].Tags), true
		}
	}

	return nil, false
}

func sdkTagMap(tags []ec2types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tg := range tags {
		m[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	return m
}

func describeTagsFor(ctx context.Context, t *testing.T, c *ec2.Client, id string) map[string]string {
	t.Helper()

	out, err := c.DescribeTags(ctx, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{{Name: aws.String("resource-id"), Values: []string{id}}},
	})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	m := map[string]string{}
	for _, td := range out.Tags {
		m[aws.ToString(td.Key)] = aws.ToString(td.Value) + "|" + string(td.ResourceType)
	}

	return m
}

// assertTagRoundTrip drives CreateTags -> Describe (by tag filter and via
// DescribeTags) -> DeleteTags -> Describe for one resource id through the real
// SDK client. describe returns the resource's tags and whether the filtered
// Describe call returned it at all.
func assertTagRoundTrip(
	ctx context.Context, t *testing.T, c *ec2.Client, id, resourceType string,
	describe func(fs []ec2types.Filter) (map[string]string, bool),
) {
	t.Helper()

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags:      []ec2types.Tag{{Key: aws.String("owner"), Value: aws.String("team-a")}},
	}); err != nil {
		t.Fatalf("CreateTags(%s): %v", id, err)
	}

	for _, fs := range tagOnlyFilters("owner", "team-a") {
		tags, found := describe(fs)
		if !found {
			t.Fatalf("%s not returned for filter %s after CreateTags", id, aws.ToString(fs[0].Name))
		}

		if tags["owner"] != "team-a" || tags["Name"] != "created" {
			t.Fatalf("%s tags = %v, want owner=team-a merged with create-time Name=created", id, tags)
		}
	}

	if got := describeTagsFor(ctx, t, c, id); got["owner"] != "team-a|"+resourceType {
		t.Fatalf("DescribeTags(%s) = %v, want owner=team-a|%s", id, got, resourceType)
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{id},
		Tags:      []ec2types.Tag{{Key: aws.String("owner")}},
	}); err != nil {
		t.Fatalf("DeleteTags(%s): %v", id, err)
	}

	if _, found := describe(tagOnlyFilters("owner", "team-a")[0]); found {
		t.Fatalf("%s still matches tag:owner after DeleteTags", id)
	}

	tags, found := describe(nil)
	if !found || tags["Name"] != "created" {
		t.Fatalf("%s after DeleteTags: found=%v tags=%v, want Name=created kept", id, found, tags)
	}

	if _, ok := tags["owner"]; ok {
		t.Fatalf("%s still carries owner after DeleteTags: %v", id, tags)
	}
}

// TestCreateTagsOnElasticIP pins that an Elastic IP allocation id can be
// tagged and untagged after creation, and that the tags land on the same record
// AllocateAddress TagSpecifications populate. Before the fix, CreateTags on an
// eipalloc- id fell through to the compute tagger and answered InvalidID.NotFound.
func TestCreateTagsOnElasticIP(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeElasticIp,
			Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("created")}},
		}},
	})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	id := aws.ToString(alloc.AllocationId)

	assertTagRoundTrip(ctx, t, c, id, "elastic-ip", func(fs []ec2types.Filter) (map[string]string, bool) {
		return addressTagged(ctx, t, c, id, fs)
	})
}

// TestCreateTagsOnVPCEndpoint is the vpce- counterpart of
// TestCreateTagsOnElasticIP.
func TestCreateTagsOnVPCEndpoint(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	ep, err := c.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		VpcId:           vpc.Vpc.VpcId,
		ServiceName:     aws.String("com.amazonaws.us-east-1.s3"),
		VpcEndpointType: ec2types.VpcEndpointTypeGateway,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpcEndpoint,
			Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("created")}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	id := aws.ToString(ep.VpcEndpoint.VpcEndpointId)

	assertTagRoundTrip(ctx, t, c, id, "vpc-endpoint", func(fs []ec2types.Filter) (map[string]string, bool) {
		return endpointTagged(ctx, t, c, id, fs)
	})
}

// TestCreateTagsOnMissingAddressingIDs pins that tagging an Elastic IP or VPC
// endpoint id that does not exist is a NotFound, not a silent success.
func TestCreateTagsOnMissingAddressingIDs(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	for _, id := range []string{"eipalloc-0000000000000000", "vpce-0000000000000000"} {
		_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{id},
			Tags:      []ec2types.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
		})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidID.NotFound" {
			t.Fatalf("CreateTags(%s) err = %v, want InvalidID.NotFound", id, err)
		}
	}
}

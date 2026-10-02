package ec2_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

func assertTagAPIErr(t *testing.T, err error, code, what string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("%s err = %v, want %s", what, err, code)
	}
}

// TestCreateTagsOnMissingAddressingIDs pins that tagging or untagging an Elastic
// IP, VPC endpoint or VPC endpoint service id that does not exist fails with the
// resource-specific NotFound code the EC2 error reference defines for it (and
// that this package already returns from the resource's own actions), not a
// silent success and not the generic InvalidID.NotFound.
func TestCreateTagsOnMissingAddressingIDs(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	for id, code := range map[string]string{
		"eipalloc-0000000000000000": "InvalidAllocationID.NotFound",
		"vpce-0000000000000000":     "InvalidVpcEndpointId.NotFound",
		"vpce-svc-0000000000000000": "InvalidVpcEndpointServiceId.NotFound",
	} {
		_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{id},
			Tags:      []ec2types.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
		})
		assertTagAPIErr(t, err, code, "CreateTags("+id+")")

		_, err = c.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: []string{id}})
		assertTagAPIErr(t, err, code, "DeleteTags("+id+")")
	}
}

// newTaggedEIP allocates an Elastic IP carrying tags from create time.
func newTaggedEIP(ctx context.Context, t *testing.T, c *ec2.Client, tags map[string]string) string {
	t.Helper()

	spec := make([]ec2types.Tag, 0, len(tags))
	for k, v := range tags {
		spec = append(spec, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeElasticIp, Tags: spec,
		}},
	})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	return aws.ToString(alloc.AllocationId)
}

// newTaggedEndpoint creates a Gateway VPC endpoint carrying tags from create time.
func newTaggedEndpoint(ctx context.Context, t *testing.T, c *ec2.Client, tags map[string]string) string {
	t.Helper()

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	spec := make([]ec2types.Tag, 0, len(tags))
	for k, v := range tags {
		spec = append(spec, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	ep, err := c.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		VpcId:           vpc.Vpc.VpcId,
		ServiceName:     aws.String("com.amazonaws.us-east-1.s3"),
		VpcEndpointType: ec2types.VpcEndpointTypeGateway,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpcEndpoint, Tags: spec,
		}},
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	return aws.ToString(ep.VpcEndpoint.VpcEndpointId)
}

// TestDeleteTagsWithoutTagsClearsUserTags pins EC2 DeleteTags with the Tag
// parameter omitted: "we delete all user-defined tags for the specified
// resources". Before the fix it answered success and left every tag in place on
// eipalloc- and vpce- ids, while the same call on an i- id cleared them.
func TestDeleteTagsWithoutTagsClearsUserTags(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	eip := newTaggedEIP(ctx, t, c, map[string]string{"Name": "created", "env": "prod"})
	ep := newTaggedEndpoint(ctx, t, c, map[string]string{"Name": "created", "env": "prod"})

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: []string{eip, ep}}); err != nil {
		t.Fatalf("DeleteTags: %v", err)
	}

	if tags, _ := addressTagged(ctx, t, c, eip, nil); len(tags) != 0 {
		t.Fatalf("eip tags after DeleteTags with no Tag = %v, want none", tags)
	}

	if tags, _ := endpointTagged(ctx, t, c, ep, nil); len(tags) != 0 {
		t.Fatalf("endpoint tags after DeleteTags with no Tag = %v, want none", tags)
	}
}

// TestDeleteTagsMatchesValue pins the DeleteTags Tag.N.Value rule: a key sent
// without a value deletes the tag whatever its value, a key sent with a value
// deletes it only when the value matches, and an explicit empty value matches
// only an empty-valued tag. Before the fix Key=env,Value=wrong deleted env.
func TestDeleteTagsMatchesValue(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	eip := newTaggedEIP(ctx, t, c, map[string]string{"env": "prod", "blank": "", "owner": "a"})

	del := func(tags ...ec2types.Tag) {
		t.Helper()

		if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: []string{eip}, Tags: tags}); err != nil {
			t.Fatalf("DeleteTags: %v", err)
		}
	}

	del(ec2types.Tag{Key: aws.String("env"), Value: aws.String("wrong")},
		ec2types.Tag{Key: aws.String("owner"), Value: aws.String("")})

	if tags, _ := addressTagged(ctx, t, c, eip, nil); tags["env"] != "prod" || tags["owner"] != "a" {
		t.Fatalf("tags after value-mismatched DeleteTags = %v, want env=prod and owner=a kept", tags)
	}

	del(ec2types.Tag{Key: aws.String("env"), Value: aws.String("prod")},
		ec2types.Tag{Key: aws.String("blank"), Value: aws.String("")})

	tags, _ := addressTagged(ctx, t, c, eip, nil)
	if _, ok := tags["env"]; ok {
		t.Fatalf("env kept after DeleteTags Key=env,Value=prod: %v", tags)
	}

	if _, ok := tags["blank"]; ok {
		t.Fatalf("blank kept after DeleteTags Key=blank,Value=\"\": %v", tags)
	}

	del(ec2types.Tag{Key: aws.String("owner")})

	if tags, _ := addressTagged(ctx, t, c, eip, nil); len(tags) != 0 {
		t.Fatalf("tags after key-only DeleteTags = %v, want none", tags)
	}
}

// TestTagBatchIsAtomic pins that CreateTags and DeleteTags check every
// ResourceId before writing: a batch naming one unknown id fails with nothing
// changed on the ids that do exist. Before the fix the EIP listed ahead of the
// bogus id was tagged (and untagged) anyway.
func TestTagBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	eip := newTaggedEIP(ctx, t, c, map[string]string{"Name": "created"})
	ep := newTaggedEndpoint(ctx, t, c, map[string]string{"Name": "created"})
	batch := []string{eip, "vpce-0000000000000bad", ep}

	_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: batch,
		Tags:      []ec2types.Tag{{Key: aws.String("owner"), Value: aws.String("a")}},
	})
	assertTagAPIErr(t, err, "InvalidVpcEndpointId.NotFound", "CreateTags(mixed batch)")

	if tags, _ := addressTagged(ctx, t, c, eip, nil); tags["owner"] != "" {
		t.Fatalf("eip tagged by a failed CreateTags batch: %v", tags)
	}

	_, err = c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: batch,
		Tags:      []ec2types.Tag{{Key: aws.String("Name")}},
	})
	assertTagAPIErr(t, err, "InvalidVpcEndpointId.NotFound", "DeleteTags(mixed batch)")

	if tags, _ := addressTagged(ctx, t, c, eip, nil); tags["Name"] != "created" {
		t.Fatalf("eip untagged by a failed DeleteTags batch: %v", tags)
	}
}

// numberedTags returns n tags k<start>..k<start+n-1>.
func numberedTags(start, n int) []ec2types.Tag {
	out := make([]ec2types.Tag, 0, n)
	for i := start; i < start+n; i++ {
		out = append(out, ec2types.Tag{Key: aws.String("k" + strconv.Itoa(i)), Value: aws.String("v")})
	}

	return out
}

// TestCreateTagsCountsExistingTags pins the 50-tag limit per resource: the tags
// a resource already carries count toward it (an overwritten key counts once,
// and "aws:" tags do not count). Before the fix only the request was counted, so
// two CreateTags calls could leave a resource with more than 50 tags.
func TestCreateTagsCountsExistingTags(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	eip := newTaggedEIP(ctx, t, c, map[string]string{"Name": "created"})

	create := func(tags []ec2types.Tag) error {
		_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{eip}, Tags: tags})
		return err
	}

	if err := create(numberedTags(0, 40)); err != nil {
		t.Fatalf("CreateTags(40): %v", err)
	}

	// 41 on the resource + 10 new = 51.
	assertTagAPIErr(t, create(numberedTags(40, 10)), "TagLimitExceeded", "CreateTags(41+10)")

	// 41 + 9 new + overwrite of k0 = 50, the limit itself.
	if err := create(append(numberedTags(40, 9), numberedTags(0, 1)...)); err != nil {
		t.Fatalf("CreateTags to exactly 50: %v", err)
	}

	if tags, _ := addressTagged(ctx, t, c, eip, nil); len(tags) != 50 {
		t.Fatalf("eip has %d tags, want 50", len(tags))
	}

	assertTagAPIErr(t, create(numberedTags(100, 1)), "TagLimitExceeded", "CreateTags(51st)")
}

// TestCreateTagsRejectsOversizedKeyAndValue pins the EC2 tag restrictions of
// 128 characters per key and 256 per value, counted in Unicode characters, not
// bytes. Before the fix any length was accepted.
func TestCreateTagsRejectsOversizedKeyAndValue(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	eip := newTaggedEIP(ctx, t, c, nil)

	create := func(k, v string) error {
		_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{eip},
			Tags:      []ec2types.Tag{{Key: aws.String(k), Value: aws.String(v)}},
		})

		return err
	}

	if err := create(strings.Repeat("é", 128), strings.Repeat("é", 256)); err != nil {
		t.Fatalf("CreateTags at the 128/256-character limits: %v", err)
	}

	assertTagAPIErr(t, create(strings.Repeat("k", 129), "v"), "InvalidParameterValue", "CreateTags(129-char key)")
	assertTagAPIErr(t, create("k", strings.Repeat("v", 257)), "InvalidParameterValue", "CreateTags(257-char value)")
}

// TestTagsOnVPCEndpointServiceOverTheWire covers the vpce-svc- id on the wire:
// CreateTags/DeleteTags reach the endpoint service record, and DescribeTags
// reports it as resource-type vpc-endpoint-service. It also pins the
// resource-type filter: each type returns only its own resources.
func TestTagsOnVPCEndpointServiceOverTheWire(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	svcOut, err := c.CreateVpcEndpointServiceConfiguration(ctx, &ec2.CreateVpcEndpointServiceConfigurationInput{
		NetworkLoadBalancerArns: []string{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/n/1"},
	})
	if err != nil {
		t.Fatalf("CreateVpcEndpointServiceConfiguration: %v", err)
	}

	svc := aws.ToString(svcOut.ServiceConfiguration.ServiceId)
	eip := newTaggedEIP(ctx, t, c, nil)
	ep := newTaggedEndpoint(ctx, t, c, nil)

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{svc, eip, ep},
		Tags:      []ec2types.Tag{{Key: aws.String("owner"), Value: aws.String("a")}},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	byType := func(types ...string) map[string]string {
		t.Helper()

		out, err := c.DescribeTags(ctx, &ec2.DescribeTagsInput{Filters: []ec2types.Filter{
			{Name: aws.String("resource-type"), Values: types},
			{Name: aws.String("key"), Values: []string{"owner"}},
		}})
		if err != nil {
			t.Fatalf("DescribeTags(%v): %v", types, err)
		}

		got := map[string]string{}
		for _, td := range out.Tags {
			got[aws.ToString(td.ResourceId)] = string(td.ResourceType)
		}

		return got
	}

	for typ, id := range map[string]string{"vpc-endpoint-service": svc, "elastic-ip": eip, "vpc-endpoint": ep} {
		if got := byType(typ); len(got) != 1 || got[id] != typ {
			t.Fatalf("DescribeTags(resource-type=%s) = %v, want only %s", typ, got, id)
		}
	}

	if got := byType("vpc-endpoint-service", "elastic-ip"); len(got) != 2 || got[svc] == "" || got[eip] == "" {
		t.Fatalf("DescribeTags(resource-type=vpc-endpoint-service,elastic-ip) = %v, want %s and %s", got, svc, eip)
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{svc}, Tags: []ec2types.Tag{{Key: aws.String("owner")}},
	}); err != nil {
		t.Fatalf("DeleteTags(%s): %v", svc, err)
	}

	cfgs, err := c.DescribeVpcEndpointServiceConfigurations(ctx, &ec2.DescribeVpcEndpointServiceConfigurationsInput{
		ServiceIds: []string{svc},
	})
	if err != nil || len(cfgs.ServiceConfigurations) != 1 {
		t.Fatalf("DescribeVpcEndpointServiceConfigurations: %v %v", cfgs, err)
	}

	if tags := sdkTagMap(cfgs.ServiceConfigurations[0].Tags); len(tags) != 0 {
		t.Fatalf("endpoint service tags after DeleteTags = %v, want none", tags)
	}
}

// TestDeleteTagsSemanticsOnEveryTagger drives the same DeleteTags rules through
// the other taggers the handler dispatches to (compute for i-, the networking
// provider's own methods for vpc- and subnet-): a value mismatch keeps the tag,
// and omitting Tag clears every user tag.
func TestDeleteTagsSemanticsOnEveryTagger(t *testing.T) {
	ctx := context.Background()
	c, _ := newTagServer(t)

	run, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	subnet, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: vpc.Vpc.VpcId, CidrBlock: aws.String("10.1.1.0/24")})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	ids := []string{aws.ToString(run.Instances[0].InstanceId), aws.ToString(vpc.Vpc.VpcId), aws.ToString(subnet.Subnet.SubnetId)}

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: ids,
		Tags: []ec2types.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("owner"), Value: aws.String("a")},
		},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: ids, Tags: []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("wrong")}},
	}); err != nil {
		t.Fatalf("DeleteTags(value mismatch): %v", err)
	}

	for _, id := range ids {
		if got := describeTagsFor(ctx, t, c, id); len(got) != 2 {
			t.Fatalf("%s tags after value-mismatched DeleteTags = %v, want env and owner kept", id, got)
		}
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: ids}); err != nil {
		t.Fatalf("DeleteTags(no Tag): %v", err)
	}

	for _, id := range ids {
		if got := describeTagsFor(ctx, t, c, id); len(got) != 0 {
			t.Fatalf("%s tags after DeleteTags with no Tag = %v, want none", id, got)
		}
	}
}

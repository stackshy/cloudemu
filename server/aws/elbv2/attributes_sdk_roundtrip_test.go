package elbv2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newELBClient(t *testing.T) *awselbv2.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awselbv2.NewFromConfig(cfg)
}

func mkLB(t *testing.T, c *awselbv2.Client, name string, tags []elbv2types.Tag) string {
	t.Helper()

	out, err := c.CreateLoadBalancer(context.Background(), &awselbv2.CreateLoadBalancerInput{
		Name:    aws.String(name),
		Type:    elbv2types.LoadBalancerTypeEnumNetwork,
		Subnets: []string{"subnet-1", "subnet-2"},
		Tags:    tags,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	return aws.ToString(out.LoadBalancers[0].LoadBalancerArn)
}

// Cross-zone is not one of the attributes the emulator models as a typed
// field, so this also pins that an unrecognized key is genuinely stored rather
// than echoed back and dropped.
func TestModifyLoadBalancerAttributesPersistsUnknownKey(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)
	arn := mkLB(t, c, "nlb-attrs", nil)

	if _, err := c.ModifyLoadBalancerAttributes(ctx, &awselbv2.ModifyLoadBalancerAttributesInput{
		LoadBalancerArn: aws.String(arn),
		Attributes: []elbv2types.LoadBalancerAttribute{{
			Key:   aws.String("load_balancing.cross_zone.enabled"),
			Value: aws.String("true"),
		}},
	}); err != nil {
		t.Fatalf("ModifyLoadBalancerAttributes: %v", err)
	}

	got, err := c.DescribeLoadBalancerAttributes(ctx,
		&awselbv2.DescribeLoadBalancerAttributesInput{LoadBalancerArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeLoadBalancerAttributes: %v", err)
	}

	found := ""

	for _, a := range got.Attributes {
		if aws.ToString(a.Key) == "load_balancing.cross_zone.enabled" {
			found = aws.ToString(a.Value)
		}
	}

	if found != "true" {
		t.Errorf("cross_zone = %q, want true (attributes: %+v)", found, got.Attributes)
	}
}

// AWS treats this as a partial update. A caller enabling cross-zone must not
// silently clear an idle timeout it set earlier.
func TestModifyLoadBalancerAttributesMerges(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)
	arn := mkLB(t, c, "nlb-merge", nil)

	set := func(key, value string) {
		t.Helper()

		if _, err := c.ModifyLoadBalancerAttributes(ctx,
			&awselbv2.ModifyLoadBalancerAttributesInput{
				LoadBalancerArn: aws.String(arn),
				Attributes: []elbv2types.LoadBalancerAttribute{
					{Key: aws.String(key), Value: aws.String(value)},
				},
			}); err != nil {
			t.Fatalf("Modify(%s): %v", key, err)
		}
	}

	set("idle_timeout.timeout_seconds", "120")
	set("load_balancing.cross_zone.enabled", "true")

	got, err := c.DescribeLoadBalancerAttributes(ctx,
		&awselbv2.DescribeLoadBalancerAttributesInput{LoadBalancerArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	attrs := map[string]string{}
	for _, a := range got.Attributes {
		attrs[aws.ToString(a.Key)] = aws.ToString(a.Value)
	}

	if attrs["idle_timeout.timeout_seconds"] != "120" {
		t.Errorf("idle timeout was clobbered by the second write: %+v", attrs)
	}

	if attrs["load_balancing.cross_zone.enabled"] != "true" {
		t.Errorf("cross_zone missing: %+v", attrs)
	}
}

// A sweep for orphaned infrastructure identifies its own load balancers by
// tag; an empty answer reads as "not mine" and leaves the orphan standing.
// TestAddAndRemoveTags is a regression guard for issue #319: AddTags/RemoveTags
// were unimplemented, so tags could only be set at create time.
func TestAddAndRemoveTags(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)

	arn := mkLB(t, c, "nlb-mut", nil)

	if _, err := c.AddTags(ctx, &awselbv2.AddTagsInput{
		ResourceArns: []string{arn},
		Tags:         []elbv2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	got, err := c.DescribeTags(ctx, &awselbv2.DescribeTagsInput{ResourceArns: []string{arn}})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	if len(got.TagDescriptions) != 1 || len(got.TagDescriptions[0].Tags) != 1 ||
		aws.ToString(got.TagDescriptions[0].Tags[0].Key) != "env" {
		t.Fatalf("after AddTags: %+v", got.TagDescriptions)
	}

	if _, err := c.RemoveTags(ctx, &awselbv2.RemoveTagsInput{
		ResourceArns: []string{arn}, TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}

	got, err = c.DescribeTags(ctx, &awselbv2.DescribeTagsInput{ResourceArns: []string{arn}})
	if err != nil {
		t.Fatalf("DescribeTags after remove: %v", err)
	}

	if len(got.TagDescriptions) == 1 && len(got.TagDescriptions[0].Tags) != 0 {
		t.Fatalf("tags remained after RemoveTags: %+v", got.TagDescriptions[0].Tags)
	}
}

func TestDescribeTagsReturnsLoadBalancerTags(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)

	arn := mkLB(t, c, "nlb-tagged", []elbv2types.Tag{
		{Key: aws.String("managed-by"), Value: aws.String("true")},
	})

	got, err := c.DescribeTags(ctx, &awselbv2.DescribeTagsInput{ResourceArns: []string{arn}})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	if len(got.TagDescriptions) != 1 {
		t.Fatalf("tag descriptions = %d, want 1", len(got.TagDescriptions))
	}

	if aws.ToString(got.TagDescriptions[0].ResourceArn) != arn {
		t.Errorf("arn = %q, want %q", aws.ToString(got.TagDescriptions[0].ResourceArn), arn)
	}

	found := false

	for _, tag := range got.TagDescriptions[0].Tags {
		if aws.ToString(tag.Key) == "managed-by" && aws.ToString(tag.Value) == "true" {
			found = true
		}
	}

	if !found {
		t.Errorf("tag not returned: %+v", got.TagDescriptions[0].Tags)
	}
}

// A caller waiting for a delete to settle polls DescribeLoadBalancers with the
// ARN until it errors. An empty list with no error leaves it polling to its
// timeout over a load balancer that is already gone — which is exactly what a
// teardown reports as "still present".
func TestDescribeDeletedLoadBalancerIsNotFound(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)
	arn := mkLB(t, c, "nlb-gone", nil)

	if _, err := c.DeleteLoadBalancer(ctx,
		&awselbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}

	_, err := c.DescribeLoadBalancers(ctx, &awselbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{arn},
	})
	if err == nil {
		t.Fatal("describing a deleted load balancer by ARN must error, not return an empty list")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "LoadBalancerNotFound" {
		t.Errorf("error code = %v, want LoadBalancerNotFound", err)
	}
}

// An unfiltered describe still reports whatever exists, including nothing.
func TestDescribeAllLoadBalancersEmptyIsNotAnError(t *testing.T) {
	c := newELBClient(t)

	out, err := c.DescribeLoadBalancers(context.Background(),
		&awselbv2.DescribeLoadBalancersInput{})
	if err != nil {
		t.Fatalf("unfiltered describe should not error: %v", err)
	}

	if len(out.LoadBalancers) != 0 {
		t.Errorf("expected no load balancers, got %d", len(out.LoadBalancers))
	}
}

// DescribeTags takes load balancers and target groups in one call and does not
// care which is which. Resolving every ARN as a load balancer made a target
// group ARN report LoadBalancerNotFound instead of its tags.
func TestDescribeTagsResolvesTargetGroups(t *testing.T) {
	ctx := context.Background()
	c := newELBClient(t)

	lbARN := mkLB(t, c, "mixed-lb", []elbv2types.Tag{
		{Key: aws.String("owner"), Value: aws.String("lb")},
	})

	tg, err := c.CreateTargetGroup(ctx, &awselbv2.CreateTargetGroupInput{
		Name:     aws.String("mixed-tg"),
		Port:     aws.Int32(80),
		Protocol: elbv2types.ProtocolEnumTcp,
		VpcId:    aws.String("vpc-1"),
		Tags: []elbv2types.Tag{
			{Key: aws.String("owner"), Value: aws.String("tg")},
		},
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tg.TargetGroups[0].TargetGroupArn)

	got, err := c.DescribeTags(ctx, &awselbv2.DescribeTagsInput{
		ResourceArns: []string{lbARN, tgARN},
	})
	if err != nil {
		t.Fatalf("DescribeTags across kinds: %v", err)
	}

	owners := map[string]string{}

	for _, td := range got.TagDescriptions {
		for _, tag := range td.Tags {
			if aws.ToString(tag.Key) == "owner" {
				owners[aws.ToString(td.ResourceArn)] = aws.ToString(tag.Value)
			}
		}
	}

	if owners[lbARN] != "lb" {
		t.Errorf("load balancer tags missing: %+v", owners)
	}

	if owners[tgARN] != "tg" {
		t.Errorf("target group tags missing: %+v", owners)
	}
}

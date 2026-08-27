package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newTagServer wires a full AWS server (EC2 query catch-all + ELBv2, which
// registers first) so both DescribeTags owners are live and the credential
// scope-gate decides which one claims a request.
func newTagServer(t *testing.T) (*ec2.Client, *awselbv2.Client) {
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

	return ec2.NewFromConfig(cfg), awselbv2.NewFromConfig(cfg)
}

// TestDescribeTagsReturnsEC2InstanceTag pins that an EC2-scoped DescribeTags
// (signed with the ec2 credential) falls through the elbv2 gate to the EC2
// handler and reports tags applied via CreateTags — the regression the elbv2
// gate would otherwise turn into a 400 InvalidAction.
func TestDescribeTagsReturnsEC2InstanceTag(t *testing.T) {
	ctx := context.Background()
	ec2c, _ := newTagServer(t)

	run, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)

	if _, err := ec2c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("web-1")},
			{Key: aws.String("env"), Value: aws.String("prod")},
		},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	out, err := ec2c.DescribeTags(ctx, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("resource-id"),
			Values: []string{id},
		}},
	})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	got := map[string]string{}
	for _, td := range out.Tags {
		if aws.ToString(td.ResourceId) != id {
			t.Fatalf("tag on unexpected resource %q, want %q", aws.ToString(td.ResourceId), id)
		}
		if string(td.ResourceType) != "instance" {
			t.Fatalf("resourceType = %q, want instance", string(td.ResourceType))
		}
		got[aws.ToString(td.Key)] = aws.ToString(td.Value)
	}

	if got["Name"] != "web-1" || got["env"] != "prod" {
		t.Fatalf("tags = %v, want Name=web-1 env=prod", got)
	}
}

// TestDescribeTagsKeyFilterNarrows pins the SDK key filter on the EC2 path.
func TestDescribeTagsKeyFilterNarrows(t *testing.T) {
	ctx := context.Background()
	ec2c, _ := newTagServer(t)

	run, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)

	if _, err := ec2c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("web-1")},
			{Key: aws.String("env"), Value: aws.String("prod")},
		},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	out, err := ec2c.DescribeTags(ctx, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("key"),
			Values: []string{"env"},
		}},
	})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	if len(out.Tags) != 1 || aws.ToString(out.Tags[0].Key) != "env" {
		t.Fatalf("key filter returned %d tags %v, want only env", len(out.Tags), out.Tags)
	}
}

// TestDescribeTagsELBScopedRoutesToELBv2 pins that an ELBv2-scoped DescribeTags
// (signed with the elasticloadbalancing credential) still routes to the elbv2
// handler — the gate must keep claiming its own DescribeTags, not defer every
// call to EC2.
func TestDescribeTagsELBScopedRoutesToELBv2(t *testing.T) {
	ctx := context.Background()
	_, elbc := newTagServer(t)

	create, err := elbc.CreateLoadBalancer(ctx, &awselbv2.CreateLoadBalancerInput{
		Name:    aws.String("nlb-tags"),
		Type:    elbv2types.LoadBalancerTypeEnumNetwork,
		Subnets: []string{"subnet-1", "subnet-2"},
		Tags:    []elbv2types.Tag{{Key: aws.String("team"), Value: aws.String("payments")}},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	arn := aws.ToString(create.LoadBalancers[0].LoadBalancerArn)

	out, err := elbc.DescribeTags(ctx, &awselbv2.DescribeTagsInput{
		ResourceArns: []string{arn},
	})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}

	if len(out.TagDescriptions) != 1 {
		t.Fatalf("TagDescriptions = %d, want 1 (routed to elbv2)", len(out.TagDescriptions))
	}

	got := map[string]string{}
	for _, tag := range out.TagDescriptions[0].Tags {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got["team"] != "payments" {
		t.Fatalf("elbv2 tags = %v, want team=payments", got)
	}
}

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

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSGServer(t *testing.T) *ec2.Client {
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

	return ec2.NewFromConfig(cfg)
}

func createSGTestVPC(t *testing.T, ctx context.Context, c *ec2.Client, cidr string) string {
	t.Helper()

	out, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	return aws.ToString(out.Vpc.VpcId)
}

// TestCreateSecurityGroupReturnsTags pins that CreateSecurityGroup echoes the
// tags applied via TagSpecifications back in its response, matching real EC2.
func TestCreateSecurityGroupReturnsTags(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	out, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web-sg"),
		Description: aws.String("web tier"),
		VpcId:       aws.String(vpcID),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSecurityGroup,
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("web-1")},
				{Key: aws.String("env"), Value: aws.String("prod")},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	got := map[string]string{}
	for _, tag := range out.Tags {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got["Name"] != "web-1" || got["env"] != "prod" {
		t.Fatalf("CreateSecurityGroup response tags = %v, want Name=web-1 env=prod", got)
	}
}

// TestCreateSecurityGroupDuplicateName pins InvalidGroup.Duplicate when a group
// name is reused within one VPC, and that the same name in a different VPC is
// allowed.
func TestCreateSecurityGroupDuplicateName(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	mk := func(vpc string) error {
		_, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String("dup-sg"),
			Description: aws.String("d"),
			VpcId:       aws.String(vpc),
		})

		return err
	}

	if err := mk(vpcID); err != nil {
		t.Fatalf("first CreateSecurityGroup: %v", err)
	}

	if err := mk(vpcID); err == nil {
		t.Fatalf("duplicate group name in same VPC should fail with InvalidGroup.Duplicate")
	}

	otherVPC := createSGTestVPC(t, ctx, c, "10.1.0.0/16")
	if err := mk(otherVPC); err != nil {
		t.Fatalf("same name in a different VPC should succeed, got: %v", err)
	}
}

// TestDescribeSecurityGroupsFilters pins that Describe honors group-name,
// vpc-id, and tag filters instead of returning every group across all VPCs.
func TestDescribeSecurityGroupsFilters(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcA := createSGTestVPC(t, ctx, c, "10.0.0.0/16")
	vpcB := createSGTestVPC(t, ctx, c, "10.1.0.0/16")

	mk := func(name, vpc, team string) {
		_, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(name),
			Description: aws.String(name),
			VpcId:       aws.String(vpc),
			TagSpecifications: []ec2types.TagSpecification{{
				ResourceType: ec2types.ResourceTypeSecurityGroup,
				Tags:         []ec2types.Tag{{Key: aws.String("team"), Value: aws.String(team)}},
			}},
		})
		if err != nil {
			t.Fatalf("CreateSecurityGroup %s: %v", name, err)
		}
	}

	mk("app-a", vpcA, "payments")
	mk("app-b", vpcB, "billing")

	byVPC, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcA}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups vpc-id: %v", err)
	}

	if len(byVPC.SecurityGroups) != 1 || aws.ToString(byVPC.SecurityGroups[0].GroupName) != "app-a" {
		t.Fatalf("vpc-id filter = %d groups, want only app-a", len(byVPC.SecurityGroups))
	}

	byName, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-name"), Values: []string{"app-b"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups group-name: %v", err)
	}

	if len(byName.SecurityGroups) != 1 || aws.ToString(byName.SecurityGroups[0].VpcId) != vpcB {
		t.Fatalf("group-name filter = %d groups, want only app-b", len(byName.SecurityGroups))
	}

	byTag, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:team"), Values: []string{"payments"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups tag: %v", err)
	}

	if len(byTag.SecurityGroups) != 1 || aws.ToString(byTag.SecurityGroups[0].GroupName) != "app-a" {
		t.Fatalf("tag:team filter = %d groups, want only app-a", len(byTag.SecurityGroups))
	}
}

// TestAuthorizeSecurityGroupIngressDuplicate pins InvalidPermission.Duplicate
// when the same ingress rule is authorized twice.
func TestAuthorizeSecurityGroupIngressDuplicate(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("sg"),
		Description: aws.String("sg"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	perm := ec2types.IpPermission{
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int32(443),
		ToPort:     aws.Int32(443),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}

	auth := &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       sg.GroupId,
		IpPermissions: []ec2types.IpPermission{perm},
	}

	if _, err := c.AuthorizeSecurityGroupIngress(ctx, auth); err != nil {
		t.Fatalf("first AuthorizeSecurityGroupIngress: %v", err)
	}

	if _, err := c.AuthorizeSecurityGroupIngress(ctx, auth); err == nil {
		t.Fatalf("duplicate ingress rule should fail with InvalidPermission.Duplicate")
	}
}

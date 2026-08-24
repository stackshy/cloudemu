package ec2_test

import (
	"context"
	"net/http/httptest"
	"strings"
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

// authorizeIngressSG creates a group and returns its id.
func authorizeIngressSG(t *testing.T, ctx context.Context, c *ec2.Client) string {
	t.Helper()

	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("sg"),
		Description: aws.String("sg"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	return aws.ToString(sg.GroupId)
}

// TestAuthorizeSecurityGroupIngressReturnsRuleSet pins that Authorize returns
// the created SecurityGroupRule set (sgr- id, isEgress=false, target fields),
// matching real EC2's response so IaC tools can track the rule by id.
func TestAuthorizeSecurityGroupIngressReturnsRuleSet(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	out, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(443),
			ToPort:     aws.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("https")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	if len(out.SecurityGroupRules) != 1 {
		t.Fatalf("Authorize returned %d rules, want 1", len(out.SecurityGroupRules))
	}

	rule := out.SecurityGroupRules[0]
	if id := aws.ToString(rule.SecurityGroupRuleId); !strings.HasPrefix(id, "sgr-") {
		t.Fatalf("SecurityGroupRuleId = %q, want sgr- prefix", id)
	}

	if aws.ToBool(rule.IsEgress) {
		t.Fatalf("ingress rule reported isEgress=true")
	}

	if got := aws.ToString(rule.CidrIpv4); got != "0.0.0.0/0" {
		t.Fatalf("CidrIpv4 = %q, want 0.0.0.0/0", got)
	}

	if got := aws.ToString(rule.Description); got != "https" {
		t.Fatalf("Description = %q, want https", got)
	}
}

// TestAuthorizeSecurityGroupIngressReferencedGroup pins that a source-group
// reference (UserIdGroupPairs) round-trips: Authorize returns referencedGroupInfo
// and DescribeSecurityGroups surfaces it under the permission's <groups>.
func TestAuthorizeSecurityGroupIngressReferencedGroup(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	out, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol:       aws.String("tcp"),
			FromPort:         aws.Int32(80),
			ToPort:           aws.Int32(80),
			UserIdGroupPairs: []ec2types.UserIdGroupPair{{GroupId: aws.String("sg-source123")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	if len(out.SecurityGroupRules) != 1 || out.SecurityGroupRules[0].ReferencedGroupInfo == nil {
		t.Fatalf("Authorize did not return referencedGroupInfo: %+v", out.SecurityGroupRules)
	}

	if got := aws.ToString(out.SecurityGroupRules[0].ReferencedGroupInfo.GroupId); got != "sg-source123" {
		t.Fatalf("referencedGroupInfo.GroupId = %q, want sg-source123", got)
	}

	desc, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{groupID}})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	if len(desc.SecurityGroups) != 1 || len(desc.SecurityGroups[0].IpPermissions) != 1 {
		t.Fatalf("unexpected Describe shape: %+v", desc.SecurityGroups)
	}

	pairs := desc.SecurityGroups[0].IpPermissions[0].UserIdGroupPairs
	if len(pairs) != 1 || aws.ToString(pairs[0].GroupId) != "sg-source123" {
		t.Fatalf("Describe UserIdGroupPairs = %+v, want sg-source123", pairs)
	}
}

// TestAuthorizeSecurityGroupIngressIPv6AndPrefixList pins that IPv6 ranges and
// prefix-list targets round-trip through Authorize and DescribeSecurityGroups.
func TestAuthorizeSecurityGroupIngressIPv6AndPrefixList(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	if _, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol:    aws.String("tcp"),
			FromPort:      aws.Int32(22),
			ToPort:        aws.Int32(22),
			Ipv6Ranges:    []ec2types.Ipv6Range{{CidrIpv6: aws.String("::/0"), Description: aws.String("v6")}},
			PrefixListIds: []ec2types.PrefixListId{{PrefixListId: aws.String("pl-12345")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	desc, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{groupID}})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	perm := desc.SecurityGroups[0].IpPermissions[0]

	if len(perm.Ipv6Ranges) != 1 || aws.ToString(perm.Ipv6Ranges[0].CidrIpv6) != "::/0" {
		t.Fatalf("Ipv6Ranges = %+v, want ::/0", perm.Ipv6Ranges)
	}

	if got := aws.ToString(perm.Ipv6Ranges[0].Description); got != "v6" {
		t.Fatalf("Ipv6Ranges[0].Description = %q, want v6", got)
	}

	if len(perm.PrefixListIds) != 1 || aws.ToString(perm.PrefixListIds[0].PrefixListId) != "pl-12345" {
		t.Fatalf("PrefixListIds = %+v, want pl-12345", perm.PrefixListIds)
	}
}

// TestDescribeSecurityGroupRules pins the DescribeSecurityGroupRules action:
// it flattens ingress + egress rules (including the default egress rule) into
// SecurityGroupRule items and honors the group-id filter.
func TestDescribeSecurityGroupRules(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	if _, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(443),
			ToPort:     aws.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	out, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-id"), Values: []string{groupID}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules: %v", err)
	}

	// One ingress rule just added + one default egress rule.
	if len(out.SecurityGroupRules) != 2 {
		t.Fatalf("DescribeSecurityGroupRules = %d rules, want 2: %+v", len(out.SecurityGroupRules), out.SecurityGroupRules)
	}

	var haveIngress, haveEgress bool
	for _, r := range out.SecurityGroupRules {
		if !strings.HasPrefix(aws.ToString(r.SecurityGroupRuleId), "sgr-") {
			t.Fatalf("rule missing sgr- id: %+v", r)
		}

		if aws.ToBool(r.IsEgress) {
			haveEgress = true
		} else {
			haveIngress = true
		}
	}

	if !haveIngress || !haveEgress {
		t.Fatalf("want both ingress and egress rules, got ingress=%v egress=%v", haveIngress, haveEgress)
	}

	// Filtering by a single rule id returns just that rule.
	oneID := aws.ToString(out.SecurityGroupRules[0].SecurityGroupRuleId)

	one, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		SecurityGroupRuleIds: []string{oneID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules by id: %v", err)
	}

	if len(one.SecurityGroupRules) != 1 || aws.ToString(one.SecurityGroupRules[0].SecurityGroupRuleId) != oneID {
		t.Fatalf("rule-id filter = %+v, want only %s", one.SecurityGroupRules, oneID)
	}
}

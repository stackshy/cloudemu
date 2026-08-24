package ec2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

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

// TestDefaultSecurityGroupOverWire pins that a freshly-created VPC surfaces its
// auto-created "default" group on DescribeSecurityGroups — allow-all egress and
// a self-referencing ingress rule (UserIdGroupPairs) — and that the group is
// non-deletable, answering Client.CannotDelete just like real EC2.
func TestDefaultSecurityGroupOverWire(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	out, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	if len(out.SecurityGroups) != 1 {
		t.Fatalf("new VPC = %d security groups, want 1 (default)", len(out.SecurityGroups))
	}

	sg := out.SecurityGroups[0]
	if aws.ToString(sg.GroupName) != "default" {
		t.Fatalf("GroupName = %q, want default", aws.ToString(sg.GroupName))
	}

	if aws.ToString(sg.Description) != "default VPC security group" {
		t.Fatalf("Description = %q, want default VPC security group", aws.ToString(sg.Description))
	}

	if len(sg.IpPermissionsEgress) != 1 || aws.ToString(sg.IpPermissionsEgress[0].IpProtocol) != "-1" ||
		len(sg.IpPermissionsEgress[0].IpRanges) != 1 ||
		aws.ToString(sg.IpPermissionsEgress[0].IpRanges[0].CidrIp) != "0.0.0.0/0" {
		t.Fatalf("egress = %+v, want single allow-all 0.0.0.0/0", sg.IpPermissionsEgress)
	}

	if len(sg.IpPermissions) != 1 || len(sg.IpPermissions[0].UserIdGroupPairs) != 1 ||
		aws.ToString(sg.IpPermissions[0].UserIdGroupPairs[0].GroupId) != aws.ToString(sg.GroupId) {
		t.Fatalf("ingress = %+v, want single self-referencing UserIdGroupPair", sg.IpPermissions)
	}

	_, err = c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: sg.GroupId})
	if err == nil {
		t.Fatal("deleting the default security group should be refused")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "Client.CannotDelete" {
		t.Fatalf("delete error = %v, want Client.CannotDelete", err)
	}
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

	// vpcA holds its auto-created "default" group plus app-a.
	if len(byVPC.SecurityGroups) != 2 {
		t.Fatalf("vpc-id filter = %d groups, want 2 (default + app-a)", len(byVPC.SecurityGroups))
	}

	var haveAppA, haveDefault bool
	for _, sg := range byVPC.SecurityGroups {
		switch aws.ToString(sg.GroupName) {
		case "app-a":
			haveAppA = true
		case "default":
			haveDefault = true
		}
	}

	if !haveAppA || !haveDefault {
		t.Fatalf("vpc-id filter groups = %+v, want default + app-a", byVPC.SecurityGroups)
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
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSecurityGroupRule,
			Tags:         []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
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

			if got := tagValue(r.Tags, "env"); got != "prod" {
				t.Fatalf("ingress rule env tag = %q, want prod: %+v", got, r.Tags)
			}
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

// TestAuthorizeSecurityGroupRuleTags pins that Authorize{Ingress,Egress} accepts
// TagSpecifications(security-group-rule) and echoes the tags on each returned
// SecurityGroupRule, matching real EC2.
func TestAuthorizeSecurityGroupRuleTags(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	out, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(22),
			ToPort:     aws.Int32(22),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
		}},
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSecurityGroupRule,
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("ssh")},
				{Key: aws.String("team"), Value: aws.String("platform")},
			},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	if len(out.SecurityGroupRules) != 1 {
		t.Fatalf("Authorize returned %d rules, want 1: %+v", len(out.SecurityGroupRules), out.SecurityGroupRules)
	}

	rule := out.SecurityGroupRules[0]
	if got := tagValue(rule.Tags, "Name"); got != "ssh" {
		t.Fatalf("rule Name tag = %q, want ssh: %+v", got, rule.Tags)
	}

	if got := tagValue(rule.Tags, "team"); got != "platform" {
		t.Fatalf("rule team tag = %q, want platform: %+v", got, rule.Tags)
	}
}

// TestDescribeSecurityGroupRulesTagFilter pins that DescribeSecurityGroupRules
// selects rules by tag:<key> value and tag-key presence, ignoring untagged and
// non-matching rules.
func TestDescribeSecurityGroupRulesTagFilter(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	// A tagged rule and an untagged rule in the same group.
	tagged, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(443),
			ToPort:     aws.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSecurityGroupRule,
			Tags:         []ec2types.Tag{{Key: aws.String("tier"), Value: aws.String("web")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress (tagged): %v", err)
	}

	if _, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(22),
			ToPort:     aws.Int32(22),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress (untagged): %v", err)
	}

	wantID := aws.ToString(tagged.SecurityGroupRules[0].SecurityGroupRuleId)

	// tag:<key>=value selects only the tagged rule.
	byValue, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:tier"), Values: []string{"web"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules tag:tier: %v", err)
	}

	if len(byValue.SecurityGroupRules) != 1 || aws.ToString(byValue.SecurityGroupRules[0].SecurityGroupRuleId) != wantID {
		t.Fatalf("tag:tier filter = %+v, want only %s", byValue.SecurityGroupRules, wantID)
	}

	// tag-key presence selects only the tagged rule.
	byKey, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag-key"), Values: []string{"tier"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules tag-key: %v", err)
	}

	if len(byKey.SecurityGroupRules) != 1 || aws.ToString(byKey.SecurityGroupRules[0].SecurityGroupRuleId) != wantID {
		t.Fatalf("tag-key filter = %+v, want only %s", byKey.SecurityGroupRules, wantID)
	}
}

// TestCreateTagsSecurityGroupRule pins that CreateTags/DeleteTags address a
// security-group rule by its sgr- id, verified through DescribeSecurityGroupRules.
func TestCreateTagsSecurityGroupRule(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	authz, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(443),
			ToPort:     aws.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	ruleID := aws.ToString(authz.SecurityGroupRules[0].SecurityGroupRuleId)

	if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{ruleID},
		Tags:      []ec2types.Tag{{Key: aws.String("owner"), Value: aws.String("net")}},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	rule := describeSGRuleByID(t, ctx, c, ruleID)
	if got := tagValue(rule.Tags, "owner"); got != "net" {
		t.Fatalf("after CreateTags owner tag = %q, want net: %+v", got, rule.Tags)
	}

	if _, err := c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{ruleID},
		Tags:      []ec2types.Tag{{Key: aws.String("owner")}},
	}); err != nil {
		t.Fatalf("DeleteTags: %v", err)
	}

	rule = describeSGRuleByID(t, ctx, c, ruleID)
	if got := tagValue(rule.Tags, "owner"); got != "" {
		t.Fatalf("after DeleteTags owner tag = %q, want empty: %+v", got, rule.Tags)
	}
}

// describeSGRuleByID returns the single rule with the given sgr- id.
func describeSGRuleByID(t *testing.T, ctx context.Context, c *ec2.Client, ruleID string) ec2types.SecurityGroupRule {
	t.Helper()

	out, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		SecurityGroupRuleIds: []string{ruleID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules by id %s: %v", ruleID, err)
	}

	if len(out.SecurityGroupRules) != 1 {
		t.Fatalf("DescribeSecurityGroupRules by id %s = %d rules, want 1", ruleID, len(out.SecurityGroupRules))
	}

	return out.SecurityGroupRules[0]
}

// authorizeIngressRuleID authorizes one ingress rule on a fresh group and
// returns the group id and the minted sgr- rule id, so the rule-mutation
// actions have a concrete target to operate on.
func authorizeIngressRuleID(t *testing.T, ctx context.Context, c *ec2.Client) (groupID, ruleID string) {
	t.Helper()

	groupID = authorizeIngressSG(t, ctx, c)

	out, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(22),
			ToPort:     aws.Int32(22),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("old")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	return groupID, aws.ToString(out.SecurityGroupRules[0].SecurityGroupRuleId)
}

// TestModifySecurityGroupRulesOverWire pins ec2:ModifySecurityGroupRules: the
// rule's permission fields (protocol, ports, target CIDR, description) are
// full-replaced while its sgr- id is preserved, matching real EC2.
func TestModifySecurityGroupRulesOverWire(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ruleID := authorizeIngressRuleID(t, ctx, c)

	if _, err := c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId: aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{{
			SecurityGroupRuleId: aws.String(ruleID),
			SecurityGroupRule: &ec2types.SecurityGroupRuleRequest{
				IpProtocol:  aws.String("tcp"),
				FromPort:    aws.Int32(8080),
				ToPort:      aws.Int32(8080),
				CidrIpv4:    aws.String("10.0.0.0/24"),
				Description: aws.String("new"),
			},
		}},
	}); err != nil {
		t.Fatalf("ModifySecurityGroupRules: %v", err)
	}

	got := describeSGRuleByID(t, ctx, c, ruleID)
	if aws.ToString(got.SecurityGroupRuleId) != ruleID {
		t.Fatalf("rule id changed: got %q, want %q", aws.ToString(got.SecurityGroupRuleId), ruleID)
	}

	if p := aws.ToInt32(got.FromPort); p != 8080 {
		t.Fatalf("FromPort = %d, want 8080", p)
	}

	if p := aws.ToInt32(got.ToPort); p != 8080 {
		t.Fatalf("ToPort = %d, want 8080", p)
	}

	if cidr := aws.ToString(got.CidrIpv4); cidr != "10.0.0.0/24" {
		t.Fatalf("CidrIpv4 = %q, want 10.0.0.0/24", cidr)
	}

	if d := aws.ToString(got.Description); d != "new" {
		t.Fatalf("Description = %q, want new", d)
	}
}

// TestModifySecurityGroupRulesUnknownRuleId pins that modifying an unknown rule
// id answers InvalidSecurityGroupRuleId.NotFound, distinct from the group-level
// InvalidGroup.NotFound.
func TestModifySecurityGroupRulesUnknownRuleId(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	_, err := c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId: aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{{
			SecurityGroupRuleId: aws.String("sgr-doesnotexist"),
			SecurityGroupRule: &ec2types.SecurityGroupRuleRequest{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(22),
				ToPort:     aws.Int32(22),
				CidrIpv4:   aws.String("0.0.0.0/0"),
			},
		}},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidSecurityGroupRuleId.NotFound" {
		t.Fatalf("err = %v, want InvalidSecurityGroupRuleId.NotFound", err)
	}
}

// TestModifySecurityGroupRulesMultipleTargets pins that a SecurityGroupRuleRequest
// naming more than one target is rejected with InvalidParameterValue, matching
// AWS's "exactly one target" rule.
func TestModifySecurityGroupRulesMultipleTargets(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ruleID := authorizeIngressRuleID(t, ctx, c)

	_, err := c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId: aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{{
			SecurityGroupRuleId: aws.String(ruleID),
			SecurityGroupRule: &ec2types.SecurityGroupRuleRequest{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(22),
				ToPort:     aws.Int32(22),
				CidrIpv4:   aws.String("0.0.0.0/0"),
				CidrIpv6:   aws.String("::/0"),
			},
		}},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("err = %v, want InvalidParameterValue", err)
	}
}

// TestUpdateSecurityGroupRuleDescriptionsIngressByRuleId pins setting then
// clearing a rule's description by its sgr- id (omitting Description clears it).
func TestUpdateSecurityGroupRuleDescriptionsIngressByRuleId(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ruleID := authorizeIngressRuleID(t, ctx, c)

	if _, err := c.UpdateSecurityGroupRuleDescriptionsIngress(ctx,
		&ec2.UpdateSecurityGroupRuleDescriptionsIngressInput{
			GroupId: aws.String(groupID),
			SecurityGroupRuleDescriptions: []ec2types.SecurityGroupRuleDescription{{
				SecurityGroupRuleId: aws.String(ruleID),
				Description:         aws.String("ssh access"),
			}},
		}); err != nil {
		t.Fatalf("UpdateSecurityGroupRuleDescriptionsIngress (set): %v", err)
	}

	if d := aws.ToString(describeSGRuleByID(t, ctx, c, ruleID).Description); d != "ssh access" {
		t.Fatalf("Description = %q, want ssh access", d)
	}

	// Omitting Description clears it.
	if _, err := c.UpdateSecurityGroupRuleDescriptionsIngress(ctx,
		&ec2.UpdateSecurityGroupRuleDescriptionsIngressInput{
			GroupId: aws.String(groupID),
			SecurityGroupRuleDescriptions: []ec2types.SecurityGroupRuleDescription{{
				SecurityGroupRuleId: aws.String(ruleID),
			}},
		}); err != nil {
		t.Fatalf("UpdateSecurityGroupRuleDescriptionsIngress (clear): %v", err)
	}

	if d := aws.ToString(describeSGRuleByID(t, ctx, c, ruleID).Description); d != "" {
		t.Fatalf("Description = %q, want empty after clear", d)
	}
}

// TestUpdateSecurityGroupRuleDescriptionsEgressByIpPermissions pins the classic
// permission-match path: the egress rule is resolved by its IpPermissions and
// its description is set, on the egress list only.
func TestUpdateSecurityGroupRuleDescriptionsEgressByIpPermissions(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := authorizeIngressSG(t, ctx, c)

	auth, err := c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(443),
			ToPort:     aws.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
		}},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupEgress: %v", err)
	}

	ruleID := aws.ToString(auth.SecurityGroupRules[0].SecurityGroupRuleId)

	if _, err := c.UpdateSecurityGroupRuleDescriptionsEgress(ctx,
		&ec2.UpdateSecurityGroupRuleDescriptionsEgressInput{
			GroupId: aws.String(groupID),
			IpPermissions: []ec2types.IpPermission{{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(443),
				ToPort:     aws.Int32(443),
				IpRanges: []ec2types.IpRange{{
					CidrIp:      aws.String("10.0.0.0/16"),
					Description: aws.String("internal https"),
				}},
			}},
		}); err != nil {
		t.Fatalf("UpdateSecurityGroupRuleDescriptionsEgress: %v", err)
	}

	if d := aws.ToString(describeSGRuleByID(t, ctx, c, ruleID).Description); d != "internal https" {
		t.Fatalf("Description = %q, want internal https", d)
	}
}

// TestUpdateSecurityGroupRuleDescriptionsMissingGroupId pins that a missing
// GroupId is rejected with InvalidParameterValue.
func TestUpdateSecurityGroupRuleDescriptionsMissingGroupId(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)

	_, err := c.UpdateSecurityGroupRuleDescriptionsIngress(ctx,
		&ec2.UpdateSecurityGroupRuleDescriptionsIngressInput{
			SecurityGroupRuleDescriptions: []ec2types.SecurityGroupRuleDescription{{
				SecurityGroupRuleId: aws.String("sgr-x"),
				Description:         aws.String("d"),
			}},
		})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("err = %v, want InvalidParameterValue", err)
	}
}

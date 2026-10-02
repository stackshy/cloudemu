package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// TestRevokeSecurityGroupIngressByRuleID covers the SecurityGroupRuleIds form
// of Revoke, which Terraform's aws_vpc_security_group_ingress_rule uses.
func TestRevokeSecurityGroupIngressByRuleID(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ruleID := authorizeIngressRuleID(t, ctx, c)

	_, err := c.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(groupID), SecurityGroupRuleIds: []string{"sgr-missing"},
	})
	assertSGErrCode(t, "revoke unknown id", err, "InvalidSecurityGroupRuleId.NotFound")

	if _, err := c.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(groupID), SecurityGroupRuleIds: []string{ruleID},
	}); err != nil {
		t.Fatalf("RevokeSecurityGroupIngress by id: %v", err)
	}

	out, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		SecurityGroupRuleIds: []string{ruleID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules: %v", err)
	}

	if len(out.SecurityGroupRules) != 0 {
		t.Fatalf("rule %s still present after revoke: %+v", ruleID, out.SecurityGroupRules)
	}
}

// TestRevokeSecurityGroupEgressByRuleID checks the egress side looks up ids
// in the egress rules only.
func TestRevokeSecurityGroupEgressByRuleID(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ingressRuleID := authorizeIngressRuleID(t, ctx, c)

	_, err := c.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
		GroupId: aws.String(groupID), SecurityGroupRuleIds: []string{ingressRuleID},
	})
	assertSGErrCode(t, "revoke ingress id on egress", err, "InvalidSecurityGroupRuleId.NotFound")
}

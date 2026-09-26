package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func describeGroupRules(t *testing.T, ctx context.Context, c *ec2.Client, groupID string) (ingress, egress []ec2types.IpPermission) {
	t.Helper()

	out, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{groupID}})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	return out.SecurityGroups[0].IpPermissions, out.SecurityGroups[0].IpPermissionsEgress
}

func tcpPerm(from, to int32, cidr string) ec2types.IpPermission {
	return ec2types.IpPermission{
		IpProtocol: aws.String("tcp"), FromPort: aws.Int32(from), ToPort: aws.Int32(to),
		IpRanges: []ec2types.IpRange{{CidrIp: aws.String(cidr)}},
	}
}

// TestAuthorizeSecurityGroupBatchIsAtomic checks that one bad permission
// rejects the whole request and stores none of the others.
func TestAuthorizeSecurityGroupBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := newPortTestGroup(t, ctx, c)
	_, egressBefore := describeGroupRules(t, ctx, c, groupID)

	perms := []ec2types.IpPermission{tcpPerm(22, 22, "10.1.0.0/16"), tcpPerm(1, 70000, "10.2.0.0/16")}

	_, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID), IpPermissions: perms,
	})
	assertSGErrCode(t, "ingress batch", err, "InvalidParameterValue")

	_, err = c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: aws.String(groupID), IpPermissions: perms,
	})
	assertSGErrCode(t, "egress batch", err, "InvalidParameterValue")

	ingress, egress := describeGroupRules(t, ctx, c, groupID)
	if len(ingress) != 0 {
		t.Fatalf("ingress rules stored after rejected batch: %+v", ingress)
	}

	if len(egress) != len(egressBefore) {
		t.Fatalf("egress rules = %d after rejected batch, want %d", len(egress), len(egressBefore))
	}
}

// TestAuthorizeSecurityGroupTopLevelFormNeedsCidr checks the top-level form
// without CidrIp is MissingParameter and stores nothing.
func TestAuthorizeSecurityGroupTopLevelFormNeedsCidr(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := newPortTestGroup(t, ctx, c)

	_, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID), IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22),
	})
	assertSGErrCode(t, "top-level without cidr", err, "MissingParameter")

	if ingress, _ := describeGroupRules(t, ctx, c, groupID); len(ingress) != 0 {
		t.Fatalf("rule stored without a target: %+v", ingress)
	}
}

// TestModifySecurityGroupRulesBatchIsAtomic checks that a bad update, by port
// or by rule id, leaves every rule in the request unchanged.
func TestModifySecurityGroupRulesBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := newPortTestGroup(t, ctx, c)

	out, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{tcpPerm(22, 22, "10.1.0.0/16"), tcpPerm(80, 80, "10.2.0.0/16")},
	})
	if err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	first := aws.ToString(out.SecurityGroupRules[0].SecurityGroupRuleId)
	second := aws.ToString(out.SecurityGroupRules[1].SecurityGroupRuleId)

	update := func(id string, from, to int32) ec2types.SecurityGroupRuleUpdate {
		return ec2types.SecurityGroupRuleUpdate{
			SecurityGroupRuleId: aws.String(id),
			SecurityGroupRule: &ec2types.SecurityGroupRuleRequest{
				IpProtocol: aws.String("tcp"), FromPort: aws.Int32(from), ToPort: aws.Int32(to),
				CidrIpv4: aws.String("0.0.0.0/0"),
			},
		}
	}

	_, err = c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId:            aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{update(first, 2222, 2222), update(second, 0, 70000)},
	})
	assertSGErrCode(t, "modify bad port", err, "InvalidParameterValue")

	_, err = c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId:            aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{update(first, 2222, 2222), update("sgr-missing", 1, 2)},
	})
	assertSGErrCode(t, "modify unknown id", err, "InvalidSecurityGroupRuleId.NotFound")

	if got := describeSGRuleByID(t, ctx, c, first); aws.ToInt32(got.FromPort) != 22 {
		t.Fatalf("first rule changed by a rejected batch: from port %d", aws.ToInt32(got.FromPort))
	}
}

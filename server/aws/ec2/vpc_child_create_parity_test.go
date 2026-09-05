package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const missingVPC = "vpc-00000000"

// TestCreateChildMissingVPCCode pins that creating a subnet, security group, or
// route table against a VpcId that does not exist reports InvalidVpcID.NotFound
// — the VPC is the missing resource — rather than the created resource's own
// NotFound code (InvalidSubnetID/InvalidGroup/InvalidRouteTableID.NotFound),
// which would wrongly claim the not-yet-created child is absent. A user pointing
// a subnet/SG/route table at a typo'd or wrong VPC must get the VPC error.
func TestCreateChildMissingVPCCode(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)

	t.Run("CreateSubnet", func(t *testing.T) {
		_, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
			VpcId:     aws.String(missingVPC),
			CidrBlock: aws.String("10.9.0.0/24"),
		})
		if err == nil {
			t.Fatal("CreateSubnet(missing vpc) succeeded, want an error")
		}

		if got := apiCode(t, err); got != "InvalidVpcID.NotFound" {
			t.Errorf("code = %q, want InvalidVpcID.NotFound", got)
		}
	})

	t.Run("CreateSecurityGroup", func(t *testing.T) {
		_, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String("x"),
			Description: aws.String("x"),
			VpcId:       aws.String(missingVPC),
		})
		if err == nil {
			t.Fatal("CreateSecurityGroup(missing vpc) succeeded, want an error")
		}

		if got := apiCode(t, err); got != "InvalidVpcID.NotFound" {
			t.Errorf("code = %q, want InvalidVpcID.NotFound", got)
		}
	})

	t.Run("CreateRouteTable", func(t *testing.T) {
		_, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
			VpcId: aws.String(missingVPC),
		})
		if err == nil {
			t.Fatal("CreateRouteTable(missing vpc) succeeded, want an error")
		}

		if got := apiCode(t, err); got != "InvalidVpcID.NotFound" {
			t.Errorf("code = %q, want InvalidVpcID.NotFound", got)
		}
	})
}

// TestAllProtocolsRulePortShapes pins how the two describe shapes report the
// ports of an all-protocols ("-1") rule, which differ in real EC2: the
// IpPermission shape (DescribeSecurityGroups) omits fromPort/toPort entirely,
// while the SecurityGroupRule shape (DescribeSecurityGroupRules) reports -1 for
// both. The default VPC security group's allow-all egress rule exercises both.
func TestAllProtocolsRulePortShapes(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	sgs, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	if len(sgs.SecurityGroups) != 1 || len(sgs.SecurityGroups[0].IpPermissionsEgress) != 1 {
		t.Fatalf("want one default group with one egress rule, got %+v", sgs.SecurityGroups)
	}

	egress := sgs.SecurityGroups[0].IpPermissionsEgress[0]
	if aws.ToString(egress.IpProtocol) != "-1" {
		t.Fatalf("egress protocol = %q, want -1", aws.ToString(egress.IpProtocol))
	}

	// IpPermission shape: AWS omits the ports for -1, so the SDK sees nil.
	if egress.FromPort != nil {
		t.Errorf("IpPermission FromPort = %d, want nil (omitted for -1)", aws.ToInt32(egress.FromPort))
	}

	if egress.ToPort != nil {
		t.Errorf("IpPermission ToPort = %d, want nil (omitted for -1)", aws.ToInt32(egress.ToPort))
	}

	groupID := aws.ToString(sgs.SecurityGroups[0].GroupId)

	rules, err := c.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-id"), Values: []string{groupID}}},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroupRules: %v", err)
	}

	// SecurityGroupRule shape: AWS reports -1 for both ports on a -1 rule.
	for _, rule := range rules.SecurityGroupRules {
		if aws.ToString(rule.IpProtocol) != "-1" {
			continue
		}

		if aws.ToInt32(rule.FromPort) != -1 {
			t.Errorf("SecurityGroupRule FromPort = %d, want -1", aws.ToInt32(rule.FromPort))
		}

		if aws.ToInt32(rule.ToPort) != -1 {
			t.Errorf("SecurityGroupRule ToPort = %d, want -1", aws.ToInt32(rule.ToPort))
		}
	}
}

// TestTCPRulePortsUnaffected guards the -1 special-casing against a regression:
// a concrete TCP rule must still report its real ports (including in both
// describe shapes), not be collapsed to nil or -1.
func TestTCPRulePortsUnaffected(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web"),
		Description: aws.String("web"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	groupID := aws.ToString(sg.GroupId)

	_, err = c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
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

	sgs, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{groupID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}

	perms := sgs.SecurityGroups[0].IpPermissions
	if len(perms) != 1 || aws.ToInt32(perms[0].FromPort) != 443 || aws.ToInt32(perms[0].ToPort) != 443 {
		t.Fatalf("IpPermission ports = %+v, want tcp 443-443", perms)
	}
}

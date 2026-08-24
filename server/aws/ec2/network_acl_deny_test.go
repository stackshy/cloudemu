package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// aclEntry finds the entry at (ruleNumber, egress) in a network ACL, or nil.
func aclEntry(entries []ec2types.NetworkAclEntry, ruleNumber int32, egress bool) *ec2types.NetworkAclEntry {
	for i := range entries {
		if aws.ToInt32(entries[i].RuleNumber) == ruleNumber && aws.ToBool(entries[i].Egress) == egress {
			return &entries[i]
		}
	}

	return nil
}

// TestCreateNetworkAclDeniesByDefault pins that a freshly created custom network
// ACL is deny-by-default: it contains only the two unmodifiable catch-all '*'
// entries (rule 32767, deny, 0.0.0.0/0) for ingress and egress, and no allow
// rule. A user relying on a fresh custom NACL to block traffic must not get an
// allow-all subnet — the security posture must match real EC2.
func TestCreateNetworkAclDeniesByDefault(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	acl, err := c.CreateNetworkAcl(ctx, &ec2.CreateNetworkAclInput{VpcId: vpc.Vpc.VpcId})
	if err != nil {
		t.Fatalf("CreateNetworkAcl: %v", err)
	}

	entries := acl.NetworkAcl.Entries
	if len(entries) != 2 {
		t.Fatalf("fresh custom ACL entries = %d, want 2 (the '*' deny pair); entries: %+v", len(entries), entries)
	}

	for _, egress := range []bool{false, true} {
		e := aclEntry(entries, 32767, egress)
		if e == nil {
			t.Fatalf("missing '*' entry for egress=%v; entries: %+v", egress, entries)
		}

		if e.RuleAction != ec2types.RuleActionDeny {
			t.Errorf("'*' entry (egress=%v) action = %q, want deny", egress, e.RuleAction)
		}

		if got := aws.ToString(e.CidrBlock); got != "0.0.0.0/0" {
			t.Errorf("'*' entry (egress=%v) cidr = %q, want 0.0.0.0/0", egress, got)
		}
	}

	// There must be no allow rule at all on a fresh custom ACL.
	for _, e := range entries {
		if e.RuleAction == ec2types.RuleActionAllow {
			t.Errorf("fresh custom ACL unexpectedly has an allow entry: %+v", e)
		}
	}
}

// TestDefaultNetworkAclHasAllowAndDenyEntries pins that the VPC's auto-created
// default network ACL both allows all traffic (rule 100, both directions) and
// still carries the unmodifiable '*' deny catch-all (rule 32767). Tools that
// assert deny-by-default read the 32767 entry off the default ACL too.
func TestDefaultNetworkAclHasAllowAndDenyEntries(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	desc, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls: %v", err)
	}

	var entries []ec2types.NetworkAclEntry

	for _, a := range desc.NetworkAcls {
		if aws.ToBool(a.IsDefault) && aws.ToString(a.VpcId) == aws.ToString(vpc.Vpc.VpcId) {
			entries = a.Entries
			break
		}
	}

	if entries == nil {
		t.Fatalf("no default network ACL found for VPC %s", aws.ToString(vpc.Vpc.VpcId))
	}

	for _, egress := range []bool{false, true} {
		allow := aclEntry(entries, 100, egress)
		if allow == nil || allow.RuleAction != ec2types.RuleActionAllow {
			t.Errorf("default ACL missing rule 100 allow (egress=%v); entries: %+v", egress, entries)
		}

		deny := aclEntry(entries, 32767, egress)
		if deny == nil || deny.RuleAction != ec2types.RuleActionDeny {
			t.Errorf("default ACL missing rule 32767 deny (egress=%v); entries: %+v", egress, entries)
		}
	}
}

package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestNetworkACLOwnerID pins that a network ACL reports its owning account id.
// Terraform's aws_network_acl and aws_default_network_acl read ownerId; an empty
// value makes the ACL look cross-account.
func TestNetworkACLOwnerID(t *testing.T) {
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

	if got := aws.ToString(acl.NetworkAcl.OwnerId); got != "123456789012" {
		t.Errorf("CreateNetworkAcl OwnerId = %q, want 123456789012", got)
	}

	desc, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{
		NetworkAclIds: []string{aws.ToString(acl.NetworkAcl.NetworkAclId)},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls: %v", err)
	}

	if got := aws.ToString(desc.NetworkAcls[0].OwnerId); got != "123456789012" {
		t.Errorf("DescribeNetworkAcls OwnerId = %q, want 123456789012", got)
	}
}

// TestReplaceNetworkAclEntry pins the previously-undispatched
// ReplaceNetworkAclEntry action: it swaps the rule at (ruleNumber, egress) in
// place, so a caller updating an existing entry (Terraform aws_network_acl_rule
// on update) sees the new CIDR and action rather than an InvalidAction error.
func TestReplaceNetworkAclEntry(t *testing.T) {
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

	aclID := aws.ToString(acl.NetworkAcl.NetworkAclId)

	if _, err := c.CreateNetworkAclEntry(ctx, &ec2.CreateNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(200),
		Egress:       aws.Bool(false),
		Protocol:     aws.String("-1"),
		RuleAction:   ec2types.RuleActionAllow,
		CidrBlock:    aws.String("10.10.0.0/16"),
	}); err != nil {
		t.Fatalf("CreateNetworkAclEntry: %v", err)
	}

	if _, err := c.ReplaceNetworkAclEntry(ctx, &ec2.ReplaceNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(200),
		Egress:       aws.Bool(false),
		Protocol:     aws.String("-1"),
		RuleAction:   ec2types.RuleActionDeny,
		CidrBlock:    aws.String("20.20.0.0/16"),
	}); err != nil {
		t.Fatalf("ReplaceNetworkAclEntry: %v", err)
	}

	desc, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{
		NetworkAclIds: []string{aclID},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls: %v", err)
	}

	entry := findACLEntry(desc.NetworkAcls[0].Entries, 200, false)
	if entry == nil {
		t.Fatalf("rule 200 not found after replace; entries: %+v", desc.NetworkAcls[0].Entries)
	}

	if aws.ToString(entry.CidrBlock) != "20.20.0.0/16" {
		t.Errorf("replaced cidr = %q, want 20.20.0.0/16", aws.ToString(entry.CidrBlock))
	}

	if entry.RuleAction != ec2types.RuleActionDeny {
		t.Errorf("replaced action = %q, want deny", entry.RuleAction)
	}
}

func findACLEntry(entries []ec2types.NetworkAclEntry, ruleNumber int32, egress bool) *ec2types.NetworkAclEntry {
	for i := range entries {
		if aws.ToInt32(entries[i].RuleNumber) == ruleNumber && aws.ToBool(entries[i].Egress) == egress {
			return &entries[i]
		}
	}

	return nil
}

// TestDescribeNetworkAclsPaginatesAllOnce pins that DescribeNetworkAcls honors
// MaxResults/NextToken, paging every ACL exactly once with no duplicates.
func TestDescribeNetworkAclsPaginatesAllOnce(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

	for range 3 {
		if _, err := c.CreateNetworkAcl(ctx, &ec2.CreateNetworkAclInput{VpcId: aws.String(vpcID)}); err != nil {
			t.Fatalf("CreateNetworkAcl: %v", err)
		}
	}

	// The expected set is every ACL in the account — the 3 just created plus the
	// VPC's auto-created default ACL — captured via an unpaginated describe.
	all, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls(all): %v", err)
	}

	want := map[string]int{}
	for _, a := range all.NetworkAcls {
		want[aws.ToString(a.NetworkAclId)] = 0
	}

	seen := map[string]int{}

	var token *string

	for {
		out, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{
			MaxResults: aws.Int32(1),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeNetworkAcls: %v", err)
		}

		if len(out.NetworkAcls) > 1 {
			t.Fatalf("page returned %d ACLs, want at most 1", len(out.NetworkAcls))
		}

		for _, a := range out.NetworkAcls {
			seen[aws.ToString(a.NetworkAclId)]++
		}

		if aws.ToString(out.NextToken) == "" {
			break
		}

		token = out.NextToken
	}

	if len(seen) != len(want) {
		t.Fatalf("paged through %d ACLs, want %d", len(seen), len(want))
	}

	for id, n := range seen {
		if n != 1 {
			t.Fatalf("ACL %s seen %d times across pages, want 1", id, n)
		}
	}
}

// mkCustomACL creates a VPC and a custom network ACL in it, returning the ACL id.
func mkCustomACL(t *testing.T, c *ec2.Client) string {
	t.Helper()

	ctx := context.Background()

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	acl, err := c.CreateNetworkAcl(ctx, &ec2.CreateNetworkAclInput{VpcId: vpc.Vpc.VpcId})
	if err != nil {
		t.Fatalf("CreateNetworkAcl: %v", err)
	}

	return aws.ToString(acl.NetworkAcl.NetworkAclId)
}

// TestCreateDuplicateNetworkAclEntryRejected pins that a second
// CreateNetworkAclEntry at the same (ruleNumber, egress) fails with
// NetworkAclEntryAlreadyExists rather than silently creating an ambiguous
// duplicate. Updating an entry is ReplaceNetworkAclEntry, not a second create.
func TestCreateDuplicateNetworkAclEntryRejected(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	aclID := mkCustomACL(t, c)

	entry := &ec2.CreateNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(100),
		Egress:       aws.Bool(false),
		Protocol:     aws.String("-1"),
		RuleAction:   ec2types.RuleActionAllow,
		CidrBlock:    aws.String("10.10.0.0/16"),
	}

	if _, err := c.CreateNetworkAclEntry(ctx, entry); err != nil {
		t.Fatalf("first CreateNetworkAclEntry: %v", err)
	}

	_, err := c.CreateNetworkAclEntry(ctx, entry)
	if err == nil {
		t.Fatal("duplicate CreateNetworkAclEntry succeeded, want NetworkAclEntryAlreadyExists")
	}

	if code := apiCode(t, err); code != "NetworkAclEntryAlreadyExists" {
		t.Errorf("duplicate entry error code = %q, want NetworkAclEntryAlreadyExists", code)
	}

	// The ingress rule 100 must still exist exactly once (plus its egress sibling
	// is a different key, so a same-number egress create is allowed).
	if _, err := c.CreateNetworkAclEntry(ctx, &ec2.CreateNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(100),
		Egress:       aws.Bool(true),
		Protocol:     aws.String("-1"),
		RuleAction:   ec2types.RuleActionAllow,
		CidrBlock:    aws.String("10.10.0.0/16"),
	}); err != nil {
		t.Fatalf("same-number egress entry should be allowed: %v", err)
	}
}

// TestDeleteReservedNetworkAclEntryRejected pins that the immutable catch-all
// '*' entry (rule 32767) cannot be deleted, so a NACL's deny-by-default posture
// cannot be silently removed. The deny entry must survive the attempt.
func TestDeleteReservedNetworkAclEntryRejected(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	aclID := mkCustomACL(t, c)

	_, err := c.DeleteNetworkAclEntry(ctx, &ec2.DeleteNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(32767),
		Egress:       aws.Bool(false),
	})
	if err == nil {
		t.Fatal("deleting reserved rule 32767 succeeded, want an error")
	}

	desc, err := c.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{NetworkAclIds: []string{aclID}})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls: %v", err)
	}

	deny := findACLEntry(desc.NetworkAcls[0].Entries, 32767, false)
	if deny == nil || deny.RuleAction != ec2types.RuleActionDeny {
		t.Fatalf("default deny (rule 32767, ingress) missing after delete attempt; entries: %+v",
			desc.NetworkAcls[0].Entries)
	}
}

// TestModifyNonexistentNetworkAclEntryNotFound pins that deleting or replacing a
// rule number that does not exist on an ACL that does exist reports
// InvalidNetworkAclEntry.NotFound (the entry is missing) rather than
// InvalidNetworkAclID.NotFound (which would imply the ACL is missing).
func TestModifyNonexistentNetworkAclEntryNotFound(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	aclID := mkCustomACL(t, c)

	_, err := c.DeleteNetworkAclEntry(ctx, &ec2.DeleteNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(500),
		Egress:       aws.Bool(false),
	})
	if err == nil {
		t.Fatal("deleting a nonexistent entry succeeded, want InvalidNetworkAclEntry.NotFound")
	}

	if code := apiCode(t, err); code != "InvalidNetworkAclEntry.NotFound" {
		t.Errorf("delete nonexistent entry code = %q, want InvalidNetworkAclEntry.NotFound", code)
	}

	_, err = c.ReplaceNetworkAclEntry(ctx, &ec2.ReplaceNetworkAclEntryInput{
		NetworkAclId: aws.String(aclID),
		RuleNumber:   aws.Int32(500),
		Egress:       aws.Bool(false),
		Protocol:     aws.String("-1"),
		RuleAction:   ec2types.RuleActionAllow,
		CidrBlock:    aws.String("10.10.0.0/16"),
	})
	if err == nil {
		t.Fatal("replacing a nonexistent entry succeeded, want InvalidNetworkAclEntry.NotFound")
	}

	if code := apiCode(t, err); code != "InvalidNetworkAclEntry.NotFound" {
		t.Errorf("replace nonexistent entry code = %q, want InvalidNetworkAclEntry.NotFound", code)
	}
}

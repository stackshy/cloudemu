package ec2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestManagedPrefixListArnAndOwner pins that CreateManagedPrefixList returns a
// prefixListArn and ownerId. Terraform's aws_ec2_managed_prefix_list stores the
// ARN as its resource arn attribute, and rules that reference the list by ARN
// need it populated.
func TestManagedPrefixListArnAndOwner(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	out, err := c.CreateManagedPrefixList(ctx, &ec2.CreateManagedPrefixListInput{
		PrefixListName: aws.String("corp-nets"),
		AddressFamily:  aws.String("IPv4"),
		MaxEntries:     aws.Int32(5),
		Entries: []ec2types.AddPrefixListEntry{
			{Cidr: aws.String("10.0.0.0/16"), Description: aws.String("hq")},
		},
	})
	if err != nil {
		t.Fatalf("CreateManagedPrefixList: %v", err)
	}

	pl := out.PrefixList
	arn := aws.ToString(pl.PrefixListArn)

	if !strings.Contains(arn, "prefix-list/"+aws.ToString(pl.PrefixListId)) {
		t.Errorf("prefixListArn = %q, want it to reference the id", arn)
	}

	if !strings.HasPrefix(arn, "arn:aws:ec2:us-east-1:") {
		t.Errorf("prefixListArn = %q, want an ec2 us-east-1 arn", arn)
	}

	if aws.ToString(pl.OwnerId) == "" {
		t.Error("ownerId is empty")
	}
}

// TestManagedPrefixListVersionCheck pins the optimistic-concurrency guard: a
// ModifyManagedPrefixList carrying a stale CurrentVersion is rejected, while the
// matching version succeeds. Terraform sends CurrentVersion on every update, and
// a skipped check would let concurrent updates clobber each other silently.
func TestManagedPrefixListVersionCheck(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	create, err := c.CreateManagedPrefixList(ctx, &ec2.CreateManagedPrefixListInput{
		PrefixListName: aws.String("corp-nets"),
		AddressFamily:  aws.String("IPv4"),
		MaxEntries:     aws.Int32(5),
		Entries: []ec2types.AddPrefixListEntry{
			{Cidr: aws.String("10.0.0.0/16")},
		},
	})
	if err != nil {
		t.Fatalf("CreateManagedPrefixList: %v", err)
	}

	id := create.PrefixList.PrefixListId
	version := aws.ToInt64(create.PrefixList.Version)

	if _, err := c.ModifyManagedPrefixList(ctx, &ec2.ModifyManagedPrefixListInput{
		PrefixListId:   id,
		CurrentVersion: aws.Int64(version + 99),
		AddEntries: []ec2types.AddPrefixListEntry{
			{Cidr: aws.String("10.1.0.0/16")},
		},
	}); err == nil {
		t.Fatal("ModifyManagedPrefixList with stale CurrentVersion should fail, got nil error")
	}

	if _, err := c.ModifyManagedPrefixList(ctx, &ec2.ModifyManagedPrefixListInput{
		PrefixListId:   id,
		CurrentVersion: aws.Int64(version),
		AddEntries: []ec2types.AddPrefixListEntry{
			{Cidr: aws.String("10.1.0.0/16")},
		},
	}); err != nil {
		t.Fatalf("ModifyManagedPrefixList with matching CurrentVersion: %v", err)
	}
}

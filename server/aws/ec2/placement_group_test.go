package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestPlacementGroupLifecycle exercises the aws_placement_group flow: create a
// cluster group, read it back by name, and delete it.
func TestPlacementGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	create, err := client.CreatePlacementGroup(ctx, &ec2.CreatePlacementGroupInput{
		GroupName: aws.String("web-cluster"),
		Strategy:  ec2types.PlacementStrategyCluster,
	})
	if err != nil {
		t.Fatalf("CreatePlacementGroup: %v", err)
	}

	pg := create.PlacementGroup
	if aws.ToString(pg.GroupName) != "web-cluster" {
		t.Errorf("GroupName = %q, want web-cluster", aws.ToString(pg.GroupName))
	}
	if pg.Strategy != ec2types.PlacementStrategyCluster {
		t.Errorf("Strategy = %q, want cluster", pg.Strategy)
	}
	if aws.ToString(pg.GroupId) == "" {
		t.Error("CreatePlacementGroup returned empty GroupId")
	}
	if pg.State != ec2types.PlacementGroupStateAvailable {
		t.Errorf("State = %q, want available", pg.State)
	}

	desc, err := client.DescribePlacementGroups(ctx, &ec2.DescribePlacementGroupsInput{
		GroupNames: []string{"web-cluster"},
	})
	if err != nil {
		t.Fatalf("DescribePlacementGroups: %v", err)
	}
	if len(desc.PlacementGroups) != 1 {
		t.Fatalf("DescribePlacementGroups = %d, want 1", len(desc.PlacementGroups))
	}

	if _, err := client.DeletePlacementGroup(ctx, &ec2.DeletePlacementGroupInput{
		GroupName: aws.String("web-cluster"),
	}); err != nil {
		t.Fatalf("DeletePlacementGroup: %v", err)
	}
}

// TestPlacementGroupPartitionCountDefaults pins that a partition-strategy group
// gets the AWS default partition count when the caller omits it.
func TestPlacementGroupPartitionCountDefaults(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	create, err := client.CreatePlacementGroup(ctx, &ec2.CreatePlacementGroupInput{
		GroupName: aws.String("part"),
		Strategy:  ec2types.PlacementStrategyPartition,
	})
	if err != nil {
		t.Fatalf("CreatePlacementGroup: %v", err)
	}

	if got := aws.ToInt32(create.PlacementGroup.PartitionCount); got != 2 {
		t.Errorf("PartitionCount = %d, want 2 (default)", got)
	}
}

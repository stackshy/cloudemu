package aws_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// TestECSRegisterContainerInstanceComposesWithEC2 proves #159 composes with
// #300: registering an ECS container instance (with no explicit EC2 id)
// provisions a real managed EC2 instance that DescribeInstances then hides by
// default and reveals with IncludeManagedResources.
func TestECSRegisterContainerInstanceComposesWithEC2(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	ci, err := cloud.ECS.RegisterContainerInstance(ctx, ecsdriver.RegisterContainerInstanceInput{Cluster: "prod"})
	if err != nil {
		t.Fatalf("RegisterContainerInstance: %v", err)
	}

	if ci.EC2InstanceID == "" {
		t.Fatal("container instance has no backing EC2 instance id")
	}

	// The backing instance exists in EC2 and is marked managed.
	shown, err := cloud.EC2.DescribeInstances(ctx, []string{ci.EC2InstanceID}, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		t.Fatalf("DescribeInstances(include): %v", err)
	}

	if len(shown) != 1 {
		t.Fatalf("expected backing EC2 instance %q, got %d instances", ci.EC2InstanceID, len(shown))
	}

	inst := shown[0]
	if inst.Operator == nil || !inst.Operator.Managed || inst.Operator.Principal != "ecs.amazonaws.com" {
		t.Fatalf("backing instance not marked ECS-managed: %+v", inst.Operator)
	}

	if inst.Tags["aws:ec2:managed-launch"] != "ecs-managed-instances" {
		t.Fatalf("missing aws:ec2:managed-launch tag: %v", inst.Tags)
	}

	// Hidden by default once the account hides managed resources.
	if err := cloud.EC2.SetManagedResourceVisibility("hidden"); err != nil {
		t.Fatalf("SetManagedResourceVisibility: %v", err)
	}

	hidden, err := cloud.EC2.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("DescribeInstances(hidden): %v", err)
	}

	for _, i := range hidden {
		if i.ID == ci.EC2InstanceID {
			t.Fatalf("managed ECS instance %q leaked into default DescribeInstances", ci.EC2InstanceID)
		}
	}
}

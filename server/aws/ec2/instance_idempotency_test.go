package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestRunInstancesClientTokenIdempotent pins that a RunInstances retry carrying
// the same ClientToken returns the already-launched instances instead of
// provisioning new ones (real EC2 idempotency), so a retried request does not
// double-provision.
func TestRunInstancesClientTokenIdempotent(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		ClientToken:  aws.String("fixed-token-abc"),
	}

	first, err := c.RunInstances(ctx, input)
	if err != nil {
		t.Fatalf("RunInstances (first): %v", err)
	}

	second, err := c.RunInstances(ctx, input)
	if err != nil {
		t.Fatalf("RunInstances (retry): %v", err)
	}

	if len(first.Instances) != 1 || len(second.Instances) != 1 {
		t.Fatalf("unexpected instance counts: first=%d second=%d",
			len(first.Instances), len(second.Instances))
	}

	if got, want := aws.ToString(second.Instances[0].InstanceId),
		aws.ToString(first.Instances[0].InstanceId); got != want {
		t.Fatalf("retry launched a new instance %q, want same as %q", got, want)
	}

	// The account must hold exactly one instance, not two.
	all, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	total := 0
	for i := range all.Reservations {
		total += len(all.Reservations[i].Instances)
	}

	if total != 1 {
		t.Fatalf("account holds %d instances, want 1 (no double-provisioning)", total)
	}
}

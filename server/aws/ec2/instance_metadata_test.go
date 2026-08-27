package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestModifyInstanceMetadataOptionsEnforcesIMDSv2 pins that
// ModifyInstanceMetadataOptions(HttpTokens=required) updates the instance's IMDS
// settings and that DescribeInstances reflects the change — the round-trip a
// security baseline / Terraform aws_instance metadata_options update relies on.
func TestModifyInstanceMetadataOptionsEnforcesIMDSv2(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-123"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	instID := aws.ToString(run.Instances[0].InstanceId)

	// A freshly launched instance defaults to IMDSv1+v2 (optional).
	if got := run.Instances[0].MetadataOptions; got == nil || got.HttpTokens != ec2types.HttpTokensStateOptional {
		t.Fatalf("launch HttpTokens = %v, want optional", run.Instances[0].MetadataOptions)
	}

	mod, err := client.ModifyInstanceMetadataOptions(ctx, &ec2.ModifyInstanceMetadataOptionsInput{
		InstanceId:              aws.String(instID),
		HttpTokens:              ec2types.HttpTokensStateRequired,
		HttpPutResponseHopLimit: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ModifyInstanceMetadataOptions: %v", err)
	}
	if mod.InstanceMetadataOptions == nil ||
		mod.InstanceMetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired {
		t.Fatalf("Modify response HttpTokens = %v, want required", mod.InstanceMetadataOptions)
	}

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instID},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	opts := desc.Reservations[0].Instances[0].MetadataOptions
	if opts == nil {
		t.Fatal("DescribeInstances MetadataOptions is nil after modify")
	}
	if opts.HttpTokens != ec2types.HttpTokensStateRequired {
		t.Errorf("HttpTokens = %q, want required", opts.HttpTokens)
	}
	if aws.ToInt32(opts.HttpPutResponseHopLimit) != 2 {
		t.Errorf("HttpPutResponseHopLimit = %d, want 2", aws.ToInt32(opts.HttpPutResponseHopLimit))
	}
}

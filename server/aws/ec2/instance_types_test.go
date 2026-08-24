package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// TestDescribeInstanceTypesRejectsUnknown pins that an explicitly requested,
// unrecognized instance type is rejected with InvalidInstanceType (real EC2),
// not answered with a fabricated {2 vCPU, 4096 MiB} spec.
func TestDescribeInstanceTypesRejectsUnknown(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	_, err := c.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{"z9.mega"},
	})
	if err == nil {
		t.Fatal("DescribeInstanceTypes(z9.mega) succeeded, want InvalidInstanceType")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceType" {
		t.Fatalf("want InvalidInstanceType, got %v", err)
	}
}

// TestDescribeInstanceTypesReportsProcessorAndNetwork pins that a known type
// carries currentGeneration, processorInfo (architecture + clock) and networkInfo
// (performance, max ENIs, IPs per ENI) — fields real DescribeInstanceTypes
// returns and capacity planners read.
func TestDescribeInstanceTypesReportsProcessorAndNetwork(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	out, err := c.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceTypeT3Micro},
	})
	if err != nil {
		t.Fatalf("DescribeInstanceTypes: %v", err)
	}

	if len(out.InstanceTypes) != 1 {
		t.Fatalf("InstanceTypes len = %d, want 1", len(out.InstanceTypes))
	}

	it := out.InstanceTypes[0]
	if !aws.ToBool(it.CurrentGeneration) {
		t.Error("currentGeneration = false, want true for t3.micro")
	}

	if it.ProcessorInfo == nil ||
		len(it.ProcessorInfo.SupportedArchitectures) == 0 ||
		it.ProcessorInfo.SupportedArchitectures[0] != ec2types.ArchitectureTypeX8664 {
		t.Errorf("processorInfo architecture missing/wrong: %+v", it.ProcessorInfo)
	}

	if it.ProcessorInfo == nil || aws.ToFloat64(it.ProcessorInfo.SustainedClockSpeedInGhz) <= 0 {
		t.Errorf("processorInfo clock speed not set: %+v", it.ProcessorInfo)
	}

	if it.NetworkInfo == nil || aws.ToString(it.NetworkInfo.NetworkPerformance) == "" {
		t.Errorf("networkInfo networkPerformance missing: %+v", it.NetworkInfo)
	}

	if it.NetworkInfo == nil || aws.ToInt32(it.NetworkInfo.MaximumNetworkInterfaces) <= 0 {
		t.Errorf("networkInfo maximumNetworkInterfaces not set: %+v", it.NetworkInfo)
	}

	if it.NetworkInfo == nil || aws.ToInt32(it.NetworkInfo.Ipv4AddressesPerInterface) <= 0 {
		t.Errorf("networkInfo ipv4AddressesPerInterface not set: %+v", it.NetworkInfo)
	}
}

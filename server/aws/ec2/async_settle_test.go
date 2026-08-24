package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAsyncEC2 wires a full in-process AWS server with AsyncSettle enabled and a
// FakeClock the test controls, and returns a real aws-sdk-go-v2 EC2 client plus
// the clock. This exercises the actual wire protocol a real user hits.
func newAsyncEC2(t *testing.T) (*ec2.Client, *cloudconfig.FakeClock) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return ec2.NewFromConfig(cfg), fc
}

// TestAsyncSettleWireEC2 pins that a real SDK client sees pending->running and
// creating->available through the wire when AsyncSettle is on, driven by the
// FakeClock. This is the real-user counterpart to the provider-level test.
func TestAsyncSettleWireEC2(t *testing.T) {
	ctx := context.Background()
	client, fc := newAsyncEC2(t)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-12345"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)
	// RunInstances response reports pending, as real EC2 does.
	if got := run.Instances[0].State.Name; got != ec2types.InstanceStateNamePending {
		t.Fatalf("RunInstances state = %q, want pending", got)
	}

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}
	if got := desc.Reservations[0].Instances[0].State.Name; got != ec2types.InstanceStateNamePending {
		t.Fatalf("describe before settle = %q, want pending", got)
	}

	fc.Advance(3 * time.Second) // past DefaultInstanceSettle (2s)

	desc, err = client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}
	if got := desc.Reservations[0].Instances[0].State.Name; got != ec2types.InstanceStateNameRunning {
		t.Fatalf("describe after settle = %q, want running", got)
	}

	// A second, still-settling instance: the instance-state-name filter must
	// agree with the rendered state (both observe the overlay).
	run2, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-12345"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances#2: %v", err)
	}
	id2 := aws.ToString(run2.Instances[0].InstanceId)

	pendingFilter := &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
		{Name: aws.String("instance-state-name"), Values: []string{"pending"}},
	}}
	fd, err := client.DescribeInstances(ctx, pendingFilter)
	if err != nil {
		t.Fatalf("DescribeInstances(filter): %v", err)
	}
	if n := countInstances(fd); n != 1 {
		t.Fatalf("instance-state-name=pending returned %d instances, want 1 (the settling one)", n)
	}

	// Stopping the settling instance must clear the launch window: it reports
	// stopped, not a stale pending.
	if _, err := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id2}}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}
	sd, _ := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id2}})
	if got := sd.Reservations[0].Instances[0].State.Name; got != ec2types.InstanceStateNameStopped {
		t.Fatalf("stopped instance state = %q, want stopped (window must be cleared)", got)
	}

	// Volume creating->available over the wire.
	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"), Size: aws.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if vol.State != ec2types.VolumeStateCreating {
		t.Fatalf("CreateVolume state = %q, want creating", vol.State)
	}

	fc.Advance(2 * time.Second) // past DefaultVolumeSettle (1s)

	dv, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{aws.ToString(vol.VolumeId)}})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if dv.Volumes[0].State != ec2types.VolumeStateAvailable {
		t.Fatalf("volume after settle = %q, want available", dv.Volumes[0].State)
	}
}

func countInstances(out *ec2.DescribeInstancesOutput) int {
	n := 0
	for _, r := range out.Reservations {
		n += len(r.Instances)
	}
	return n
}

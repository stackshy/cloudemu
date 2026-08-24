package ec2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEC2 wires a full in-process AWS server and returns a real aws-sdk-go-v2
// EC2 client pointed at it.
func newEC2(t *testing.T) *ec2.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return ec2.NewFromConfig(cfg)
}

// TestCreateVolumeEchoesEncryptionAndPerformance pins that Encrypted, Iops,
// Throughput, and KmsKeyId survive the round-trip. Dropping them makes
// Terraform's aws_ebs_volume see perpetual drift / forced replacement.
func TestCreateVolumeEchoesEncryptionAndPerformance(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	const kmsKey = "arn:aws:kms:us-east-1:123456789012:key/abcd1234-a123-456a-a12b-a123b4cd56ef"

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(100),
		VolumeType:       ec2types.VolumeTypeGp3,
		Iops:             aws.Int32(4000),
		Throughput:       aws.Int32(250),
		Encrypted:        aws.Bool(true),
		KmsKeyId:         aws.String(kmsKey),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if !aws.ToBool(cre.Encrypted) {
		t.Errorf("CreateVolume Encrypted = false, want true")
	}
	if aws.ToInt32(cre.Iops) != 4000 {
		t.Errorf("CreateVolume Iops = %d, want 4000", aws.ToInt32(cre.Iops))
	}
	if aws.ToInt32(cre.Throughput) != 250 {
		t.Errorf("CreateVolume Throughput = %d, want 250", aws.ToInt32(cre.Throughput))
	}
	if aws.ToString(cre.KmsKeyId) != kmsKey {
		t.Errorf("CreateVolume KmsKeyId = %q, want %q", aws.ToString(cre.KmsKeyId), kmsKey)
	}

	desc, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{aws.ToString(cre.VolumeId)},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if len(desc.Volumes) != 1 {
		t.Fatalf("DescribeVolumes = %d, want 1", len(desc.Volumes))
	}

	v := desc.Volumes[0]
	if !aws.ToBool(v.Encrypted) || aws.ToInt32(v.Iops) != 4000 ||
		aws.ToInt32(v.Throughput) != 250 || aws.ToString(v.KmsKeyId) != kmsKey {
		t.Errorf("DescribeVolumes dropped attributes: encrypted=%v iops=%d throughput=%d kms=%q",
			aws.ToBool(v.Encrypted), aws.ToInt32(v.Iops), aws.ToInt32(v.Throughput), aws.ToString(v.KmsKeyId))
	}
}

// TestDescribeVolumesHonoursTagFilter pins that a tag:Name filter narrows the
// result set instead of being ignored (which returned every volume).
func TestDescribeVolumesHonoursTagFilter(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	mk := func(name string) {
		if _, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String("us-east-1a"),
			Size:             aws.Int32(10),
			TagSpecifications: []ec2types.TagSpecification{{
				ResourceType: ec2types.ResourceTypeVolume,
				Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String(name)}},
			}},
		}); err != nil {
			t.Fatalf("CreateVolume(%s): %v", name, err)
		}
	}

	mk("keep")
	mk("other")

	out, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{"keep"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if len(out.Volumes) != 1 {
		t.Fatalf("tag:Name=keep returned %d volumes, want 1", len(out.Volumes))
	}

	none, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{"nomatch"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes(nomatch): %v", err)
	}
	if len(none.Volumes) != 0 {
		t.Fatalf("tag:Name=nomatch returned %d volumes, want 0", len(none.Volumes))
	}
}

// TestDescribeVolumeStatusReturnsOK pins that the previously-undispatched
// DescribeVolumeStatus action reports a status for an existing volume.
func TestDescribeVolumeStatusReturnsOK(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(8),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	out, err := client.DescribeVolumeStatus(ctx, &ec2.DescribeVolumeStatusInput{
		VolumeIds: []string{aws.ToString(cre.VolumeId)},
	})
	if err != nil {
		t.Fatalf("DescribeVolumeStatus: %v", err)
	}
	if len(out.VolumeStatuses) != 1 {
		t.Fatalf("VolumeStatuses = %d, want 1", len(out.VolumeStatuses))
	}
	if got := out.VolumeStatuses[0].VolumeStatus.Status; got != ec2types.VolumeStatusInfoStatusOk {
		t.Errorf("volume status = %q, want ok", got)
	}
}

// TestModifyVolumeGrowsAndReportsModification pins that ModifyVolume (previously
// undispatched) returns a VolumeModification with matching original/target
// fields and that DescribeVolumes reflects the new size, IOPS, and type.
func TestModifyVolumeGrowsAndReportsModification(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(20),
		VolumeType:       ec2types.VolumeTypeGp3,
		Iops:             aws.Int32(3000),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volID := aws.ToString(cre.VolumeId)

	mod, err := client.ModifyVolume(ctx, &ec2.ModifyVolumeInput{
		VolumeId:   aws.String(volID),
		Size:       aws.Int32(50),
		Iops:       aws.Int32(6000),
		VolumeType: ec2types.VolumeTypeIo2,
	})
	if err != nil {
		t.Fatalf("ModifyVolume: %v", err)
	}

	vm := mod.VolumeModification
	if vm == nil {
		t.Fatal("ModifyVolume returned nil VolumeModification")
	}
	if aws.ToString(vm.VolumeId) != volID {
		t.Errorf("VolumeModification VolumeId = %q, want %q", aws.ToString(vm.VolumeId), volID)
	}
	if vm.ModificationState == "" {
		t.Error("VolumeModification ModificationState is empty")
	}
	if aws.ToInt32(vm.OriginalSize) != 20 || aws.ToInt32(vm.TargetSize) != 50 {
		t.Errorf("size original=%d target=%d, want 20 -> 50",
			aws.ToInt32(vm.OriginalSize), aws.ToInt32(vm.TargetSize))
	}
	if aws.ToInt32(vm.TargetIops) != 6000 {
		t.Errorf("TargetIops = %d, want 6000", aws.ToInt32(vm.TargetIops))
	}
	if vm.OriginalVolumeType != ec2types.VolumeTypeGp3 || vm.TargetVolumeType != ec2types.VolumeTypeIo2 {
		t.Errorf("volume type original=%q target=%q, want gp3 -> io2",
			vm.OriginalVolumeType, vm.TargetVolumeType)
	}

	desc, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volID}})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	v := desc.Volumes[0]
	if aws.ToInt32(v.Size) != 50 {
		t.Errorf("post-modify Size = %d, want 50", aws.ToInt32(v.Size))
	}
	if aws.ToInt32(v.Iops) != 6000 {
		t.Errorf("post-modify Iops = %d, want 6000", aws.ToInt32(v.Iops))
	}
	if v.VolumeType != ec2types.VolumeTypeIo2 {
		t.Errorf("post-modify VolumeType = %q, want io2", v.VolumeType)
	}
}

// TestModifyVolumeShrinkRejected pins that shrinking a volume is rejected with
// InvalidParameterValue rather than silently succeeding.
func TestModifyVolumeShrinkRejected(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(30),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	_, err = client.ModifyVolume(ctx, &ec2.ModifyVolumeInput{
		VolumeId: cre.VolumeId,
		Size:     aws.Int32(10),
	})
	if err == nil {
		t.Fatal("ModifyVolume(shrink) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("ModifyVolume(shrink) error = %v, want InvalidParameterValue", err)
	}
}

// TestModifyVolumeMissingIsNotFound pins the AWS error code for modifying a
// non-existent volume.
func TestModifyVolumeMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.ModifyVolume(ctx, &ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-000000000000"),
		Size:     aws.Int32(100),
	})
	if err == nil {
		t.Fatal("ModifyVolume(missing) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidVolume.NotFound" {
		t.Fatalf("ModifyVolume(missing) error = %v, want InvalidVolume.NotFound", err)
	}
}

// TestAttachVolumeCrossAZRejected pins that attaching a volume to an instance
// in a different Availability Zone is rejected with InvalidVolume.ZoneMismatch,
// matching real EC2 (a volume and its instance must share an AZ).
func TestAttachVolumeCrossAZRejected(t *testing.T) {
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

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-west-2b"),
		Size:             aws.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	_, err = client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   vol.VolumeId,
		InstanceId: aws.String(instID),
		Device:     aws.String("/dev/sdf"),
	})
	if err == nil {
		t.Fatal("cross-AZ AttachVolume succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidVolume.ZoneMismatch" {
		t.Fatalf("cross-AZ AttachVolume error = %v, want InvalidVolume.ZoneMismatch", err)
	}
}

// TestDeleteAttachedVolumeReturnsVolumeInUse pins that deleting an attached
// volume is rejected with the VolumeInUse code (not the generic IncorrectState).
func TestDeleteAttachedVolumeReturnsVolumeInUse(t *testing.T) {
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

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if _, err := client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   vol.VolumeId,
		InstanceId: aws.String(instID),
		Device:     aws.String("/dev/sdf"),
	}); err != nil {
		t.Fatalf("AttachVolume: %v", err)
	}

	_, err = client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: vol.VolumeId})
	if err == nil {
		t.Fatal("DeleteVolume(attached) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "VolumeInUse" {
		t.Fatalf("DeleteVolume(attached) error = %v, want VolumeInUse", err)
	}
}

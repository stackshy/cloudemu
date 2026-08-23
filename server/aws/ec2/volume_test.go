package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

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

package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"
)

// TestAttachVolumeMissingInstanceIsInvalidInstanceID covers that attaching an
// existing volume to a non-existent instance answers InvalidInstanceID.NotFound
// (the missing resource), not InvalidVolume.NotFound.
func TestAttachVolumeMissingInstanceIsInvalidInstanceID(t *testing.T) {
	c := newEC2Client(t)
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(8),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	_, err = c.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   vol.VolumeId,
		InstanceId: aws.String("i-0123456789abcdef0"),
		Device:     aws.String("/dev/sdf"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("AttachVolume missing instance: err = %v, want an API error", err)
	}

	if apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
		t.Fatalf("code = %q, want InvalidInstanceID.NotFound", apiErr.ErrorCode())
	}
}

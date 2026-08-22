package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSComputeCompat drives a realistic EC2 control-plane lifecycle through
// the real aws-sdk-go-v2 EC2 client and records one compat result per portable
// "compute" operation exercised. Operation names match the portable Compute
// driver in docs/coverage/coverage.json (providers.aws = "EC2"): the SDK
// ModifyInstanceAttribute call maps to the "ModifyInstance" driver op, the
// unfiltered DescribeLaunchTemplates call maps to "ListLaunchTemplates", a
// name-filtered DescribeLaunchTemplates maps to "GetLaunchTemplate",
// CancelSpotInstanceRequests maps to "CancelSpotRequests", and
// DescribeSpotInstanceRequests maps to "DescribeSpotRequests".
func TestAWSComputeCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{EC2: provider.EC2})

	client := awsec2.NewFromConfig(sess.Config(), func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc          = "compute"
		imageID      = "ami-12345678"
		zone         = "us-east-1a"
		keyName      = "compat-key"
		templateName = "compat-template"
		volumeSize   = int32(10)
		instanceOne  = int32(1)
	)

	var (
		instanceID string
		volumeID   string
		snapshotID string
		newImageID string
		spotReqID  string
	)

	sess.Op(svc, "RunInstances", func() error {
		out, err := client.RunInstances(ctx, &awsec2.RunInstancesInput{
			ImageId:      aws.String(imageID),
			InstanceType: ec2types.InstanceTypeT2Micro,
			MinCount:     aws.Int32(instanceOne),
			MaxCount:     aws.Int32(instanceOne),
		})
		if err != nil {
			return err
		}

		if len(out.Instances) == 0 {
			return fmt.Errorf("RunInstances returned no instances")
		}

		instanceID = aws.ToString(out.Instances[0].InstanceId)
		if instanceID == "" {
			return fmt.Errorf("RunInstances returned empty instance id")
		}

		return nil
	})

	sess.Op(svc, "DescribeInstances", func() error {
		out, err := client.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return err
		}

		if len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
			return fmt.Errorf("DescribeInstances did not return instance %q", instanceID)
		}

		return nil
	})

	sess.Op(svc, "CreateKeyPair", func() error {
		out, err := client.CreateKeyPair(ctx, &awsec2.CreateKeyPairInput{
			KeyName: aws.String(keyName),
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.KeyName) != keyName {
			return fmt.Errorf("CreateKeyPair returned key name %q", aws.ToString(out.KeyName))
		}

		return nil
	})

	sess.Op(svc, "DescribeKeyPairs", func() error {
		out, err := client.DescribeKeyPairs(ctx, &awsec2.DescribeKeyPairsInput{
			KeyNames: []string{keyName},
		})
		if err != nil {
			return err
		}

		if len(out.KeyPairs) == 0 {
			return fmt.Errorf("DescribeKeyPairs did not return %q", keyName)
		}

		return nil
	})

	sess.Op(svc, "DeleteKeyPair", func() error {
		_, err := client.DeleteKeyPair(ctx, &awsec2.DeleteKeyPairInput{
			KeyName: aws.String(keyName),
		})

		return err
	})

	sess.Op(svc, "CreateVolume", func() error {
		out, err := client.CreateVolume(ctx, &awsec2.CreateVolumeInput{
			AvailabilityZone: aws.String(zone),
			Size:             aws.Int32(volumeSize),
			VolumeType:       ec2types.VolumeTypeGp3,
		})
		if err != nil {
			return err
		}

		volumeID = aws.ToString(out.VolumeId)
		if volumeID == "" {
			return fmt.Errorf("CreateVolume returned empty volume id")
		}

		return nil
	})

	sess.Op(svc, "DescribeVolumes", func() error {
		out, err := client.DescribeVolumes(ctx, &awsec2.DescribeVolumesInput{
			VolumeIds: []string{volumeID},
		})
		if err != nil {
			return err
		}

		if len(out.Volumes) == 0 {
			return fmt.Errorf("DescribeVolumes did not return %q", volumeID)
		}

		return nil
	})

	sess.Op(svc, "CreateSnapshot", func() error {
		out, err := client.CreateSnapshot(ctx, &awsec2.CreateSnapshotInput{
			VolumeId:    aws.String(volumeID),
			Description: aws.String("compat snapshot"),
		})
		if err != nil {
			return err
		}

		snapshotID = aws.ToString(out.SnapshotId)
		if snapshotID == "" {
			return fmt.Errorf("CreateSnapshot returned empty snapshot id")
		}

		return nil
	})

	sess.Op(svc, "DescribeSnapshots", func() error {
		out, err := client.DescribeSnapshots(ctx, &awsec2.DescribeSnapshotsInput{
			SnapshotIds: []string{snapshotID},
		})
		if err != nil {
			return err
		}

		if len(out.Snapshots) == 0 {
			return fmt.Errorf("DescribeSnapshots did not return %q", snapshotID)
		}

		return nil
	})

	sess.Op(svc, "AttachVolume", func() error {
		_, err := client.AttachVolume(ctx, &awsec2.AttachVolumeInput{
			VolumeId:   aws.String(volumeID),
			InstanceId: aws.String(instanceID),
			Device:     aws.String("/dev/sdf"),
		})

		return err
	})

	sess.Op(svc, "DetachVolume", func() error {
		_, err := client.DetachVolume(ctx, &awsec2.DetachVolumeInput{
			VolumeId: aws.String(volumeID),
		})

		return err
	})

	sess.Op(svc, "DeleteSnapshot", func() error {
		_, err := client.DeleteSnapshot(ctx, &awsec2.DeleteSnapshotInput{
			SnapshotId: aws.String(snapshotID),
		})

		return err
	})

	sess.Op(svc, "DeleteVolume", func() error {
		_, err := client.DeleteVolume(ctx, &awsec2.DeleteVolumeInput{
			VolumeId: aws.String(volumeID),
		})

		return err
	})

	sess.Op(svc, "CreateImage", func() error {
		out, err := client.CreateImage(ctx, &awsec2.CreateImageInput{
			InstanceId: aws.String(instanceID),
			Name:       aws.String("compat-image"),
		})
		if err != nil {
			return err
		}

		newImageID = aws.ToString(out.ImageId)
		if newImageID == "" {
			return fmt.Errorf("CreateImage returned empty image id")
		}

		return nil
	})

	sess.Op(svc, "DescribeImages", func() error {
		out, err := client.DescribeImages(ctx, &awsec2.DescribeImagesInput{
			ImageIds: []string{newImageID},
		})
		if err != nil {
			return err
		}

		if len(out.Images) == 0 {
			return fmt.Errorf("DescribeImages did not return %q", newImageID)
		}

		return nil
	})

	sess.Op(svc, "DeregisterImage", func() error {
		_, err := client.DeregisterImage(ctx, &awsec2.DeregisterImageInput{
			ImageId: aws.String(newImageID),
		})

		return err
	})

	sess.Op(svc, "CreateLaunchTemplate", func() error {
		out, err := client.CreateLaunchTemplate(ctx, &awsec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(templateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String(imageID),
				InstanceType: ec2types.InstanceTypeT2Micro,
			},
		})
		if err != nil {
			return err
		}

		if out.LaunchTemplate == nil {
			return fmt.Errorf("CreateLaunchTemplate returned no template")
		}

		return nil
	})

	sess.Op(svc, "ListLaunchTemplates", func() error {
		out, err := client.DescribeLaunchTemplates(ctx, &awsec2.DescribeLaunchTemplatesInput{})
		if err != nil {
			return err
		}

		if len(out.LaunchTemplates) == 0 {
			return fmt.Errorf("DescribeLaunchTemplates returned no templates")
		}

		return nil
	})

	sess.Op(svc, "GetLaunchTemplate", func() error {
		out, err := client.DescribeLaunchTemplates(ctx, &awsec2.DescribeLaunchTemplatesInput{
			LaunchTemplateNames: []string{templateName},
		})
		if err != nil {
			return err
		}

		if len(out.LaunchTemplates) == 0 {
			return fmt.Errorf("DescribeLaunchTemplates did not return %q", templateName)
		}

		return nil
	})

	sess.Op(svc, "DeleteLaunchTemplate", func() error {
		_, err := client.DeleteLaunchTemplate(ctx, &awsec2.DeleteLaunchTemplateInput{
			LaunchTemplateName: aws.String(templateName),
		})

		return err
	})

	sess.Op(svc, "RequestSpotInstances", func() error {
		out, err := client.RequestSpotInstances(ctx, &awsec2.RequestSpotInstancesInput{
			InstanceCount: aws.Int32(instanceOne),
			SpotPrice:     aws.String("0.05"),
			LaunchSpecification: &ec2types.RequestSpotLaunchSpecification{
				ImageId:      aws.String(imageID),
				InstanceType: ec2types.InstanceTypeT2Micro,
			},
		})
		if err != nil {
			return err
		}

		if len(out.SpotInstanceRequests) == 0 {
			return fmt.Errorf("RequestSpotInstances returned no requests")
		}

		spotReqID = aws.ToString(out.SpotInstanceRequests[0].SpotInstanceRequestId)

		return nil
	})

	sess.Op(svc, "DescribeSpotRequests", func() error {
		out, err := client.DescribeSpotInstanceRequests(ctx, &awsec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{spotReqID},
		})
		if err != nil {
			return err
		}

		if len(out.SpotInstanceRequests) == 0 {
			return fmt.Errorf("DescribeSpotInstanceRequests did not return %q", spotReqID)
		}

		return nil
	})

	sess.Op(svc, "CancelSpotRequests", func() error {
		_, err := client.CancelSpotInstanceRequests(ctx, &awsec2.CancelSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{spotReqID},
		})

		return err
	})

	sess.Op(svc, "StopInstances", func() error {
		_, err := client.StopInstances(ctx, &awsec2.StopInstancesInput{
			InstanceIds: []string{instanceID},
		})

		return err
	})

	sess.Op(svc, "ModifyInstance", func() error {
		_, err := client.ModifyInstanceAttribute(ctx, &awsec2.ModifyInstanceAttributeInput{
			InstanceId:   aws.String(instanceID),
			InstanceType: &ec2types.AttributeValue{Value: aws.String("t2.large")},
		})

		return err
	})

	sess.Op(svc, "StartInstances", func() error {
		_, err := client.StartInstances(ctx, &awsec2.StartInstancesInput{
			InstanceIds: []string{instanceID},
		})

		return err
	})

	sess.Op(svc, "RebootInstances", func() error {
		_, err := client.RebootInstances(ctx, &awsec2.RebootInstancesInput{
			InstanceIds: []string{instanceID},
		})

		return err
	})

	sess.Op(svc, "TerminateInstances", func() error {
		_, err := client.TerminateInstances(ctx, &awsec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})

		return err
	})
}

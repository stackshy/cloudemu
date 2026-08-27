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

func launchInstanceForImage(t *testing.T, ctx context.Context, client *ec2.Client) string {
	t.Helper()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-base"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	return aws.ToString(run.Instances[0].InstanceId)
}

// TestCreateImagePopulatesRootDeviceAndMapping pins that an AMI created from a
// running instance carries a root device and a block device mapping that
// references the backing snapshot — both were empty before.
func TestCreateImagePopulatesRootDeviceAndMapping(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId: aws.String(instID),
		Name:       aws.String("backup-ami"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{aws.ToString(img.ImageId)},
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}
	if len(desc.Images) != 1 {
		t.Fatalf("DescribeImages = %d, want 1", len(desc.Images))
	}

	im := desc.Images[0]
	if aws.ToString(im.RootDeviceName) != "/dev/sda1" {
		t.Errorf("RootDeviceName = %q, want /dev/sda1", aws.ToString(im.RootDeviceName))
	}
	if im.RootDeviceType != ec2types.DeviceTypeEbs {
		t.Errorf("RootDeviceType = %q, want ebs", im.RootDeviceType)
	}
	if len(im.BlockDeviceMappings) != 1 {
		t.Fatalf("BlockDeviceMappings = %d, want 1", len(im.BlockDeviceMappings))
	}
	if im.BlockDeviceMappings[0].Ebs == nil || aws.ToString(im.BlockDeviceMappings[0].Ebs.SnapshotId) == "" {
		t.Errorf("block device mapping missing backing snapshot: %+v", im.BlockDeviceMappings[0])
	}
}

// TestDescribeImagesReportsMetadata pins the metadata fields that were empty or
// hardcoded: OwnerId, VirtualizationType, Hypervisor, ImageType, PlatformDetails.
func TestDescribeImagesReportsMetadata(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId: aws.String(instID),
		Name:       aws.String("meta-ami"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{aws.ToString(img.ImageId)},
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}

	im := desc.Images[0]
	if aws.ToString(im.OwnerId) == "" {
		t.Errorf("OwnerId is empty")
	}
	if im.VirtualizationType != ec2types.VirtualizationTypeHvm {
		t.Errorf("VirtualizationType = %q, want hvm", im.VirtualizationType)
	}
	if im.Hypervisor != ec2types.HypervisorTypeXen {
		t.Errorf("Hypervisor = %q, want xen", im.Hypervisor)
	}
	if im.ImageType != ec2types.ImageTypeValuesMachine {
		t.Errorf("ImageType = %q, want machine", im.ImageType)
	}
	if aws.ToString(im.PlatformDetails) == "" {
		t.Errorf("PlatformDetails is empty")
	}
}

// TestDescribeImagesOwnerFilter pins that the owner-id filter matches on the
// image's owner rather than being ignored.
func TestDescribeImagesOwnerFilter(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId: aws.String(instID),
		Name:       aws.String("owned-ami"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{aws.ToString(img.ImageId)},
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}
	owner := aws.ToString(desc.Images[0].OwnerId)

	match, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Filters: []ec2types.Filter{{Name: aws.String("owner-id"), Values: []string{owner}}},
	})
	if err != nil {
		t.Fatalf("DescribeImages(owner match): %v", err)
	}
	if len(match.Images) != 1 {
		t.Fatalf("owner-id match returned %d images, want 1", len(match.Images))
	}

	none, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Filters: []ec2types.Filter{{Name: aws.String("owner-id"), Values: []string{"999999999999"}}},
	})
	if err != nil {
		t.Fatalf("DescribeImages(owner nomatch): %v", err)
	}
	if len(none.Images) != 0 {
		t.Fatalf("owner-id nomatch returned %d images, want 0", len(none.Images))
	}
}

// TestDescribeImagesUnknownIDErrors pins that an explicit unknown AMI id
// returns InvalidAMIID.NotFound rather than an empty success.
func TestDescribeImagesUnknownIDErrors(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{"ami-000000000000"},
	})
	if err == nil {
		t.Fatalf("DescribeImages(unknown) returned no error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidAMIID.NotFound" {
		t.Fatalf("error code = %v, want InvalidAMIID.NotFound", err)
	}
}

// TestDescribeImageAttribute pins that the previously-undispatched
// DescribeImageAttribute action returns the AMI's description attribute.
func TestDescribeImageAttribute(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId:  aws.String(instID),
		Name:        aws.String("attr-ami"),
		Description: aws.String("golden image"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	out, err := client.DescribeImageAttribute(ctx, &ec2.DescribeImageAttributeInput{
		ImageId:   img.ImageId,
		Attribute: ec2types.ImageAttributeNameDescription,
	})
	if err != nil {
		t.Fatalf("DescribeImageAttribute: %v", err)
	}
	if out.Description == nil || aws.ToString(out.Description.Value) != "golden image" {
		t.Errorf("description attribute = %+v, want golden image", out.Description)
	}
}

// TestRegisterImageDescribableAndBootable pins that RegisterImage (previously
// undispatched) stores the caller-supplied architecture, root device, and block
// device mapping, that DescribeImages reflects them, and that RunInstances can
// launch from the registered AMI.
func TestRegisterImageDescribableAndBootable(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	// A backing snapshot the AMI's block device mapping references.
	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	snap, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{VolumeId: vol.VolumeId})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	reg, err := client.RegisterImage(ctx, &ec2.RegisterImageInput{
		Name:               aws.String("custom-ami"),
		Architecture:       ec2types.ArchitectureValuesArm64,
		RootDeviceName:     aws.String("/dev/xvda"),
		VirtualizationType: aws.String("hvm"),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{{
			DeviceName: aws.String("/dev/xvda"),
			Ebs: &ec2types.EbsBlockDevice{
				SnapshotId:          snap.SnapshotId,
				VolumeSize:          aws.Int32(20),
				VolumeType:          ec2types.VolumeTypeGp3,
				DeleteOnTermination: aws.Bool(true),
			},
		}},
	})
	if err != nil {
		t.Fatalf("RegisterImage: %v", err)
	}
	amiID := aws.ToString(reg.ImageId)
	if amiID == "" {
		t.Fatal("RegisterImage returned empty ImageId")
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{amiID}})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}
	if len(desc.Images) != 1 {
		t.Fatalf("DescribeImages = %d, want 1", len(desc.Images))
	}

	im := desc.Images[0]
	if im.Architecture != ec2types.ArchitectureValuesArm64 {
		t.Errorf("Architecture = %q, want arm64", im.Architecture)
	}
	if aws.ToString(im.RootDeviceName) != "/dev/xvda" {
		t.Errorf("RootDeviceName = %q, want /dev/xvda", aws.ToString(im.RootDeviceName))
	}
	if len(im.BlockDeviceMappings) != 1 {
		t.Fatalf("BlockDeviceMappings = %d, want 1", len(im.BlockDeviceMappings))
	}
	bdm := im.BlockDeviceMappings[0]
	if bdm.Ebs == nil || aws.ToString(bdm.Ebs.SnapshotId) != aws.ToString(snap.SnapshotId) {
		t.Errorf("block device mapping snapshot = %+v, want %q", bdm.Ebs, aws.ToString(snap.SnapshotId))
	}
	if aws.ToInt32(bdm.Ebs.VolumeSize) != 20 {
		t.Errorf("block device mapping size = %d, want 20", aws.ToInt32(bdm.Ebs.VolumeSize))
	}

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String(amiID),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances(registered AMI): %v", err)
	}
	if len(run.Instances) != 1 || aws.ToString(run.Instances[0].ImageId) != amiID {
		t.Fatalf("RunInstances did not launch from registered AMI %q: %+v", amiID, run.Instances)
	}
}

// TestRegisterImageDuplicateNameRejected pins the AWS error code for reusing an
// AMI name that is already registered.
func TestRegisterImageDuplicateNameRejected(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.RegisterImage(ctx, &ec2.RegisterImageInput{
		Name:         aws.String("dup-ami"),
		Architecture: ec2types.ArchitectureValuesX8664,
	}); err != nil {
		t.Fatalf("RegisterImage(first): %v", err)
	}

	_, err := client.RegisterImage(ctx, &ec2.RegisterImageInput{
		Name:         aws.String("dup-ami"),
		Architecture: ec2types.ArchitectureValuesX8664,
	})
	if err == nil {
		t.Fatal("RegisterImage(duplicate name) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidAMIName.Duplicate" {
		t.Fatalf("RegisterImage error = %v, want InvalidAMIName.Duplicate", err)
	}
}

// TestCopyImageCreatesIndependentAMI pins that CopyImage (aws_ami_copy) returns
// a new AMI id distinct from the source and carrying the requested name.
func TestCopyImageCreatesIndependentAMI(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	src, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId: aws.String(instID),
		Name:       aws.String("source-ami"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}
	srcID := aws.ToString(src.ImageId)

	cp, err := client.CopyImage(ctx, &ec2.CopyImageInput{
		SourceRegion:  aws.String("us-east-1"),
		SourceImageId: aws.String(srcID),
		Name:          aws.String("copied-ami"),
	})
	if err != nil {
		t.Fatalf("CopyImage: %v", err)
	}
	copyID := aws.ToString(cp.ImageId)
	if copyID == "" || copyID == srcID {
		t.Fatalf("CopyImage id = %q, want a new id distinct from %q", copyID, srcID)
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{copyID}})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}
	if len(desc.Images) != 1 || aws.ToString(desc.Images[0].Name) != "copied-ami" {
		t.Fatalf("copied image = %+v, want name copied-ami", desc.Images)
	}
}

// TestModifyImageAttributeLaunchPermission pins the AMI-sharing round-trip
// aws_ami_launch_permission relies on: adding a launchPermission group=all makes
// the AMI public and DescribeImageAttribute(launchPermission) returns the grant.
func TestModifyImageAttributeLaunchPermission(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	instID := launchInstanceForImage(t, ctx, client)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId: aws.String(instID),
		Name:       aws.String("shared-ami"),
	})
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}
	imgID := aws.ToString(img.ImageId)

	if _, err := client.ModifyImageAttribute(ctx, &ec2.ModifyImageAttributeInput{
		ImageId: aws.String(imgID),
		LaunchPermission: &ec2types.LaunchPermissionModifications{
			Add: []ec2types.LaunchPermission{{Group: ec2types.PermissionGroupAll}},
		},
	}); err != nil {
		t.Fatalf("ModifyImageAttribute: %v", err)
	}

	attr, err := client.DescribeImageAttribute(ctx, &ec2.DescribeImageAttributeInput{
		ImageId:   aws.String(imgID),
		Attribute: ec2types.ImageAttributeNameLaunchPermission,
	})
	if err != nil {
		t.Fatalf("DescribeImageAttribute: %v", err)
	}
	if len(attr.LaunchPermissions) != 1 || attr.LaunchPermissions[0].Group != ec2types.PermissionGroupAll {
		t.Fatalf("LaunchPermissions = %+v, want one grant group=all", attr.LaunchPermissions)
	}

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{imgID}})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}
	if !aws.ToBool(desc.Images[0].Public) {
		t.Error("image Public = false after launchPermission group=all, want true")
	}
}

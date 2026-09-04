package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// runOne launches a single instance and returns its id.
func runOne(t *testing.T, client *ec2.Client, bdms ...ec2types.BlockDeviceMapping) string {
	t.Helper()

	out, err := client.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId:             aws.String("ami-1"),
		InstanceType:        ec2types.InstanceTypeT3Micro,
		MinCount:            aws.Int32(1),
		MaxCount:            aws.Int32(1),
		BlockDeviceMappings: bdms,
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	if len(out.Instances) != 1 {
		t.Fatalf("launched %d instances, want 1", len(out.Instances))
	}

	return aws.ToString(out.Instances[0].InstanceId)
}

// volumesFor returns every volume the server reports (optionally by id).
func volumesFor(t *testing.T, client *ec2.Client, ids ...string) []ec2types.Volume {
	t.Helper()

	out, err := client.DescribeVolumes(context.Background(), &ec2.DescribeVolumesInput{VolumeIds: ids})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}

	return out.Volumes
}

// TestRunInstancesMaterializesRootVolume proves a launched instance has a real
// root EBS volume: DescribeVolumes returns it attached at the root device with
// DeleteOnTermination=true, and DescribeInstances echoes the same in its
// blockDeviceMapping (Terraform's aws_instance.root_block_device).
func TestRunInstancesMaterializesRootVolume(t *testing.T) {
	client := newEC2(t)

	id := runOne(t, client)

	vols := volumesFor(t, client)
	if len(vols) != 1 {
		t.Fatalf("volume count = %d, want 1 (the root volume)", len(vols))
	}

	root := vols[0]
	if root.State != ec2types.VolumeStateInUse {
		t.Errorf("root volume State = %q, want in-use", root.State)
	}
	if len(root.Attachments) != 1 {
		t.Fatalf("root volume attachments = %d, want 1", len(root.Attachments))
	}

	att := root.Attachments[0]
	if aws.ToString(att.InstanceId) != id {
		t.Errorf("attachment InstanceId = %q, want %q", aws.ToString(att.InstanceId), id)
	}
	if aws.ToString(att.Device) != "/dev/sda1" {
		t.Errorf("attachment Device = %q, want /dev/sda1", aws.ToString(att.Device))
	}
	if !aws.ToBool(att.DeleteOnTermination) {
		t.Errorf("root attachment DeleteOnTermination = false, want true")
	}

	// DescribeInstances must echo the same root block device mapping.
	di, err := client.DescribeInstances(context.Background(),
		&ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	inst := di.Reservations[0].Instances[0]
	if len(inst.BlockDeviceMappings) != 1 {
		t.Fatalf("instance blockDeviceMappings = %d, want 1", len(inst.BlockDeviceMappings))
	}

	ebs := inst.BlockDeviceMappings[0].Ebs
	if ebs == nil || !aws.ToBool(ebs.DeleteOnTermination) {
		t.Errorf("instance root DeleteOnTermination = false, want true")
	}
	if aws.ToString(ebs.VolumeId) != aws.ToString(root.VolumeId) {
		t.Errorf("instance root volumeId = %q, want %q", aws.ToString(ebs.VolumeId), aws.ToString(root.VolumeId))
	}
}

// TestRunInstancesHonorsRootBlockDeviceMapping proves a client-supplied mapping
// for the root device controls the root volume's size, type, and
// DeleteOnTermination (Terraform sets these under root_block_device).
func TestRunInstancesHonorsRootBlockDeviceMapping(t *testing.T) {
	client := newEC2(t)

	runOne(t, client, ec2types.BlockDeviceMapping{
		DeviceName: aws.String("/dev/sda1"),
		Ebs: &ec2types.EbsBlockDevice{
			VolumeSize:          aws.Int32(30),
			VolumeType:          ec2types.VolumeTypeGp2,
			DeleteOnTermination: aws.Bool(false),
		},
	})

	vols := volumesFor(t, client)
	if len(vols) != 1 {
		t.Fatalf("volume count = %d, want 1", len(vols))
	}

	root := vols[0]
	if aws.ToInt32(root.Size) != 30 {
		t.Errorf("root Size = %d, want 30", aws.ToInt32(root.Size))
	}
	if root.VolumeType != ec2types.VolumeTypeGp2 {
		t.Errorf("root VolumeType = %q, want gp2", root.VolumeType)
	}
	if len(root.Attachments) != 1 || aws.ToBool(root.Attachments[0].DeleteOnTermination) {
		t.Errorf("root DeleteOnTermination = true, want false (client override)")
	}
}

// TestTerminateCascadeDeletesDeleteOnTerminationVolumes proves the real EC2
// terminate cascade: the root volume (DeleteOnTermination=true) and any
// DeleteOnTermination=true data volume are deleted, while a separately attached
// DeleteOnTermination=false volume survives as `available`.
func TestTerminateCascadeDeletesDeleteOnTerminationVolumes(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	// Launch with a root volume (DoT=true default) and a data volume the client
	// marks DoT=true.
	id := runOne(t, client, ec2types.BlockDeviceMapping{
		DeviceName: aws.String("/dev/sdg"),
		Ebs: &ec2types.EbsBlockDevice{
			VolumeSize:          aws.Int32(20),
			DeleteOnTermination: aws.Bool(true),
		},
	})

	// Separately create and attach a DoT=false volume (AttachVolume defaults it
	// to false — the volume must outlive the instance).
	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	keepID := aws.ToString(cre.VolumeId)

	if _, err := client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(keepID),
		InstanceId: aws.String(id),
		Device:     aws.String("/dev/sdh"),
	}); err != nil {
		t.Fatalf("AttachVolume: %v", err)
	}

	// Three volumes now exist: root (DoT=true), data (DoT=true), keep (DoT=false).
	if got := len(volumesFor(t, client)); got != 3 {
		t.Fatalf("pre-terminate volume count = %d, want 3", got)
	}

	if _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	// Only the DoT=false volume remains, and it is back to available/detached.
	remaining := volumesFor(t, client)
	if len(remaining) != 1 {
		t.Fatalf("post-terminate volume count = %d, want 1 (the DoT=false volume)", len(remaining))
	}

	kept := remaining[0]
	if aws.ToString(kept.VolumeId) != keepID {
		t.Fatalf("surviving volume = %q, want %q", aws.ToString(kept.VolumeId), keepID)
	}
	if kept.State != ec2types.VolumeStateAvailable {
		t.Errorf("surviving volume State = %q, want available", kept.State)
	}
	if len(kept.Attachments) != 0 {
		t.Errorf("surviving volume still has %d attachments, want 0", len(kept.Attachments))
	}
}

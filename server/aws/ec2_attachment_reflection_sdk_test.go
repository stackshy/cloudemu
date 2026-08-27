package aws_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchInstanceInSubnet creates a VPC + subnet and launches a single instance
// into it, returning the client and the ids the reflection tests build on.
func launchInstanceInSubnet(t *testing.T) (client *ec2.Client, instanceID, subnetID string) {
	t.Helper()

	client = newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sub, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            vpc.Vpc.VpcId,
		CidrBlock:        aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	})
	require.NoError(t, err)
	subnetID = aws.ToString(sub.Subnet.SubnetId)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-12345"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		SubnetId:     aws.String(subnetID),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 1)

	return client, aws.ToString(run.Instances[0].InstanceId), subnetID
}

// describeInstance returns the single instance with the given id.
func describeInstance(t *testing.T, client *ec2.Client, instanceID string) ec2types.Instance {
	t.Helper()

	out, err := client.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	require.NoError(t, err)
	require.Len(t, out.Reservations, 1)
	require.Len(t, out.Reservations[0].Instances, 1)

	return out.Reservations[0].Instances[0]
}

// primaryENIID returns the id of the instance's eth0 (device index 0) interface
// as reported by DescribeNetworkInterfaces, the authoritative ENI store.
func primaryENIID(t *testing.T, client *ec2.Client, instanceID string) string {
	t.Helper()

	out, err := client.DescribeNetworkInterfaces(context.Background(), &ec2.DescribeNetworkInterfacesInput{})
	require.NoError(t, err)

	for i := range out.NetworkInterfaces {
		ni := out.NetworkInterfaces[i]
		if ni.Attachment != nil &&
			aws.ToString(ni.Attachment.InstanceId) == instanceID &&
			aws.ToInt32(ni.Attachment.DeviceIndex) == 0 {
			return aws.ToString(ni.NetworkInterfaceId)
		}
	}

	t.Fatalf("no primary ENI found for instance %q", instanceID)

	return ""
}

// TestInstanceReflectsSecondaryENI covers BUG2/BUG4: a secondary ENI attached to
// a running instance shows up in DescribeInstances alongside the primary, and
// the primary carries its real eni- id (matching DescribeNetworkInterfaces).
func TestInstanceReflectsSecondaryENI(t *testing.T) {
	client, instanceID, subnetID := launchInstanceInSubnet(t)
	ctx := context.Background()

	wantPrimary := primaryENIID(t, client, instanceID)

	eni, err := client.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	require.NoError(t, err)
	secondaryID := aws.ToString(eni.NetworkInterface.NetworkInterfaceId)

	_, err = client.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(secondaryID),
		InstanceId:         aws.String(instanceID),
		DeviceIndex:        aws.Int32(1),
	})
	require.NoError(t, err)

	inst := describeInstance(t, client, instanceID)
	require.Len(t, inst.NetworkInterfaces, 2, "instance must reflect both primary and secondary ENIs")

	byDevice := map[int32]ec2types.InstanceNetworkInterface{}
	for i := range inst.NetworkInterfaces {
		ni := inst.NetworkInterfaces[i]
		require.NotNil(t, ni.Attachment)
		byDevice[aws.ToInt32(ni.Attachment.DeviceIndex)] = ni
	}

	primary, ok := byDevice[0]
	require.True(t, ok, "primary (device index 0) interface must be present")
	// BUG2/BUG4: primary carries its real eni- id, matching the ENI store.
	assert.Equal(t, wantPrimary, aws.ToString(primary.NetworkInterfaceId))
	assert.NotEmpty(t, aws.ToString(primary.MacAddress))

	secondary, ok := byDevice[1]
	require.True(t, ok, "secondary (device index 1) interface must be present")
	assert.Equal(t, secondaryID, aws.ToString(secondary.NetworkInterfaceId))
	assert.Equal(t, ec2types.AttachmentStatusAttached, secondary.Attachment.Status)
}

// TestInstancePrimaryENIMatchesStore covers BUG4: an instance launched into a
// subnet reports a non-empty primary networkInterfaceId equal to the id
// DescribeNetworkInterfaces reports for its eth0.
func TestInstancePrimaryENIMatchesStore(t *testing.T) {
	client, instanceID, _ := launchInstanceInSubnet(t)

	want := primaryENIID(t, client, instanceID)
	require.NotEmpty(t, want)

	inst := describeInstance(t, client, instanceID)
	require.Len(t, inst.NetworkInterfaces, 1)
	assert.Equal(t, want, aws.ToString(inst.NetworkInterfaces[0].NetworkInterfaceId))
}

// TestInstanceReflectsAttachedVolume covers BUG3: an attached EBS volume shows up
// in the instance's BlockDeviceMappings with status attached.
func TestInstanceReflectsAttachedVolume(t *testing.T) {
	client, instanceID, _ := launchInstanceInSubnet(t)
	ctx := context.Background()

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
	})
	require.NoError(t, err)
	volumeID := aws.ToString(vol.VolumeId)

	_, err = client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(volumeID),
		InstanceId: aws.String(instanceID),
		Device:     aws.String("/dev/sdf"),
	})
	require.NoError(t, err)

	inst := describeInstance(t, client, instanceID)

	var found *ec2types.InstanceBlockDeviceMapping
	for i := range inst.BlockDeviceMappings {
		if inst.BlockDeviceMappings[i].Ebs != nil &&
			aws.ToString(inst.BlockDeviceMappings[i].Ebs.VolumeId) == volumeID {
			found = &inst.BlockDeviceMappings[i]
		}
	}

	require.NotNil(t, found, "attached volume must appear in BlockDeviceMappings")
	assert.Equal(t, "/dev/sdf", aws.ToString(found.DeviceName))
	assert.Equal(t, ec2types.AttachmentStatusAttached, found.Ebs.Status)
}

// TestInstanceReflectsElasticIP covers BUG1: associating an EIP to an instance
// makes DescribeInstances report the EIP's public address, and disassociating
// clears it.
func TestInstanceReflectsElasticIP(t *testing.T) {
	client, instanceID, _ := launchInstanceInSubnet(t)
	ctx := context.Background()

	addr, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{Domain: ec2types.DomainTypeVpc})
	require.NoError(t, err)
	publicIP := aws.ToString(addr.PublicIp)
	require.NotEmpty(t, publicIP)

	assoc, err := client.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: addr.AllocationId,
		InstanceId:   aws.String(instanceID),
	})
	require.NoError(t, err)

	inst := describeInstance(t, client, instanceID)
	assert.Equal(t, publicIP, aws.ToString(inst.PublicIpAddress), "instance must report the associated EIP")

	_, err = client.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
		AssociationId: assoc.AssociationId,
	})
	require.NoError(t, err)

	inst = describeInstance(t, client, instanceID)
	assert.Empty(t, aws.ToString(inst.PublicIpAddress), "public IP must clear on disassociate")
}

// TestTerminateCleansUpAttachments covers BUG1 + BUG2 terminate cleanup:
// terminating an instance detaches its secondary ENI back to available (so it
// can be deleted and the subnet drained), and disassociates its EIP while
// leaving the address allocated.
func TestTerminateCleansUpAttachments(t *testing.T) {
	client, instanceID, subnetID := launchInstanceInSubnet(t)
	ctx := context.Background()

	// Secondary ENI attached to the instance.
	eni, err := client.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	require.NoError(t, err)
	secondaryID := aws.ToString(eni.NetworkInterface.NetworkInterfaceId)

	_, err = client.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(secondaryID),
		InstanceId:         aws.String(instanceID),
		DeviceIndex:        aws.Int32(1),
	})
	require.NoError(t, err)

	// EIP associated to the instance.
	addr, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{Domain: ec2types.DomainTypeVpc})
	require.NoError(t, err)
	allocationID := aws.ToString(addr.AllocationId)

	_, err = client.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: addr.AllocationId,
		InstanceId:   aws.String(instanceID),
	})
	require.NoError(t, err)

	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	require.NoError(t, err)

	// The secondary ENI is detached back to available (not stuck in-use on the
	// dead instance) — so it can be deleted, which then unwedges DeleteSubnet.
	desc, err := client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{secondaryID},
	})
	require.NoError(t, err)
	require.Len(t, desc.NetworkInterfaces, 1)
	assert.Equal(t, ec2types.NetworkInterfaceStatusAvailable, desc.NetworkInterfaces[0].Status)
	assert.Nil(t, desc.NetworkInterfaces[0].Attachment)

	_, err = client.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(secondaryID),
	})
	require.NoError(t, err, "an available secondary ENI must be deletable after terminate")

	_, err = client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)})
	require.NoError(t, err, "subnet delete must no longer be wedged once the ENIs are drained")

	// The EIP is disassociated but still allocated.
	addrs, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{allocationID},
	})
	require.NoError(t, err)
	require.Len(t, addrs.Addresses, 1)
	assert.Empty(t, aws.ToString(addrs.Addresses[0].AssociationId), "EIP must be disassociated on terminate")
	assert.Empty(t, aws.ToString(addrs.Addresses[0].InstanceId))
}

// TestAttachVolumeToTerminatedInstanceStillErrors is a regression guard: the EBS
// attach state machine must keep rejecting an attach to a terminated instance
// (IncorrectInstanceState), unaffected by the reflection changes.
func TestAttachVolumeToTerminatedInstanceStillErrors(t *testing.T) {
	client, instanceID, _ := launchInstanceInSubnet(t)
	ctx := context.Background()

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
	})
	require.NoError(t, err)

	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	require.NoError(t, err)

	_, err = client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   vol.VolumeId,
		InstanceId: aws.String(instanceID),
		Device:     aws.String("/dev/sdf"),
	})
	require.Error(t, err, "attaching to a terminated instance must still fail")
}

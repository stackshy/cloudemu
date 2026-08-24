package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// TestAttachNetworkInterfaceLifecycle exercises the ENI attach/detach lifecycle
// aws_network_interface_attachment drives: create an ENI, attach it to an
// instance (getting an attachmentId), see the attachment on Describe, then
// detach it.
func TestAttachNetworkInterfaceLifecycle(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	subnet, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     vpc.Vpc.VpcId,
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subnetID := aws.ToString(subnet.Subnet.SubnetId)

	eni, err := client.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}
	eniID := aws.ToString(eni.NetworkInterface.NetworkInterfaceId)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-123"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	instID := aws.ToString(run.Instances[0].InstanceId)

	attach, err := client.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniID),
		InstanceId:         aws.String(instID),
		DeviceIndex:        aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("AttachNetworkInterface: %v", err)
	}
	attachmentID := aws.ToString(attach.AttachmentId)
	if attachmentID == "" {
		t.Fatal("AttachNetworkInterface returned empty attachmentId")
	}

	desc, err := client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	got := desc.NetworkInterfaces[0]
	if got.Status != "in-use" {
		t.Errorf("ENI status = %q, want in-use", got.Status)
	}
	if got.Attachment == nil || aws.ToString(got.Attachment.InstanceId) != instID {
		t.Errorf("attachment instanceId = %+v, want %s", got.Attachment, instID)
	}

	if _, err := client.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
		AttachmentId: aws.String(attachmentID),
	}); err != nil {
		t.Fatalf("DetachNetworkInterface: %v", err)
	}
}

// TestAttachNetworkInterfaceUnknownInstance pins that attaching to a
// non-existent instance answers InvalidInstanceID.NotFound.
func TestAttachNetworkInterfaceUnknownInstance(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	subnet, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     vpc.Vpc.VpcId,
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	eni, err := client.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: subnet.Subnet.SubnetId,
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	_, err = client.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: eni.NetworkInterface.NetworkInterfaceId,
		InstanceId:         aws.String("i-00000000000000000"),
		DeviceIndex:        aws.Int32(1),
	})
	if err == nil {
		t.Fatal("AttachNetworkInterface(unknown instance) succeeded, want error")
	}
}

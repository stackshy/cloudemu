package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// runInstanceInSubnet launches one instance in the subnet (optionally in the
// given security groups) and returns its id. Real EC2 gives every instance a
// primary eth0 ENI in its subnet, so this is the setup the delete guards react to.
func runInstanceInSubnet(t *testing.T, c *ec2.Client, subnetID string, groups []string) string {
	t.Helper()

	run, err := c.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId:          aws.String("ami-12345"),
		InstanceType:     ec2types.InstanceTypeT2Micro,
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SubnetId:         aws.String(subnetID),
		SecurityGroupIds: groups,
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	return aws.ToString(run.Instances[0].InstanceId)
}

// terminate terminates the instance and fails the test on error.
func terminate(t *testing.T, c *ec2.Client, instanceID string) {
	t.Helper()

	if _, err := c.TerminateInstances(context.Background(), &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}
}

// TestDeleteSubnetBlockedByRunningInstance pins the real-EC2 rule that a subnet
// holding a running instance cannot be deleted: the instance's primary ENI
// resides in the subnet, so DeleteSubnet answers DependencyViolation. Once the
// instance is terminated (releasing its ENI), the subnet deletes.
func TestDeleteSubnetBlockedByRunningInstance(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	instanceID := runInstanceInSubnet(t, c, subnetID, nil)

	if _, err := c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}); err == nil {
		t.Fatal("DeleteSubnet of a subnet with a running instance should fail")
	} else if code := apiCode(t, err); code != "DependencyViolation" {
		t.Fatalf("DeleteSubnet(running instance) code = %q, want DependencyViolation", code)
	}

	terminate(t, c, instanceID)

	if _, err := c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}); err != nil {
		t.Fatalf("DeleteSubnet after terminate should succeed: %v", err)
	}
}

// TestDeleteSecurityGroupBlockedByRunningInstance pins that a security group a
// running instance uses cannot be deleted: the instance's primary ENI is a
// member of the group, so DeleteSecurityGroup answers DependencyViolation. After
// the instance is terminated (releasing its ENI), the group deletes.
func TestDeleteSecurityGroupBlockedByRunningInstance(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, subnetID := mkVPCSubnet(t, c)

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String("app-sg"), Description: aws.String("app"), VpcId: aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := aws.ToString(sg.GroupId)

	instanceID := runInstanceInSubnet(t, c, subnetID, []string{sgID})

	if _, err := c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}); err == nil {
		t.Fatal("DeleteSecurityGroup of a group used by a running instance should fail")
	} else if code := apiCode(t, err); code != "DependencyViolation" {
		t.Fatalf("DeleteSecurityGroup(running instance) code = %q, want DependencyViolation", code)
	}

	terminate(t, c, instanceID)

	if _, err := c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}); err != nil {
		t.Fatalf("DeleteSecurityGroup after terminate should succeed: %v", err)
	}
}

// TestRunInstancesMaterializesPrimaryENI pins that RunInstances creates the
// instance's eth0 network interface the way real EC2 does: DescribeNetworkInterfaces
// (filtered by subnet-id) reports one in-use interface attached at device index 0
// and belonging to the instance.
func TestRunInstancesMaterializesPrimaryENI(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	instanceID := runInstanceInSubnet(t, c, subnetID, nil)

	desc, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{{Name: aws.String("subnet-id"), Values: []string{subnetID}}},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	if len(desc.NetworkInterfaces) != 1 {
		t.Fatalf("network interfaces for instance = %d, want 1", len(desc.NetworkInterfaces))
	}

	eni := desc.NetworkInterfaces[0]
	if aws.ToString(eni.SubnetId) != subnetID {
		t.Errorf("ENI subnetId = %q, want %q", aws.ToString(eni.SubnetId), subnetID)
	}
	if eni.Status != ec2types.NetworkInterfaceStatusInUse {
		t.Errorf("ENI status = %q, want in-use", eni.Status)
	}
	if eni.Attachment == nil || aws.ToInt32(eni.Attachment.DeviceIndex) != 0 {
		t.Errorf("ENI attachment = %+v, want deviceIndex 0", eni.Attachment)
	}
	if eni.Attachment != nil && aws.ToString(eni.Attachment.InstanceId) != instanceID {
		t.Errorf("ENI attachment instanceId = %q, want %q", aws.ToString(eni.Attachment.InstanceId), instanceID)
	}

	// After terminate the primary ENI is released, so the instance no longer
	// appears among the VPC's interfaces.
	terminate(t, c, instanceID)

	desc, err = c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{{Name: aws.String("subnet-id"), Values: []string{subnetID}}},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces after terminate: %v", err)
	}
	if len(desc.NetworkInterfaces) != 0 {
		t.Fatalf("network interfaces after terminate = %d, want 0", len(desc.NetworkInterfaces))
	}
}

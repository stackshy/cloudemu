package ec2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestDescribeInstanceReportsHardwareDetail pins the static/derived instance
// facts real EC2 returns on a DescribeInstances item — architecture, hypervisor,
// virtualization/root-device, placement, monitoring, private DNS name, and a
// primary network interface — which SDKs and IaC read.
func TestDescribeInstanceReportsHardwareDetail(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	inst := out.Reservations[0].Instances[0]

	if inst.Architecture != ec2types.ArchitectureValuesX8664 {
		t.Errorf("architecture = %q, want x86_64", inst.Architecture)
	}

	if inst.Hypervisor != ec2types.HypervisorTypeXen {
		t.Errorf("hypervisor = %q, want xen", inst.Hypervisor)
	}

	if inst.VirtualizationType != ec2types.VirtualizationTypeHvm {
		t.Errorf("virtualizationType = %q, want hvm", inst.VirtualizationType)
	}

	if inst.RootDeviceType != ec2types.DeviceTypeEbs {
		t.Errorf("rootDeviceType = %q, want ebs", inst.RootDeviceType)
	}

	if aws.ToString(inst.RootDeviceName) != "/dev/xvda" {
		t.Errorf("rootDeviceName = %q, want /dev/xvda", aws.ToString(inst.RootDeviceName))
	}

	if inst.Placement == nil || aws.ToString(inst.Placement.AvailabilityZone) == "" {
		t.Errorf("placement availabilityZone missing: %+v", inst.Placement)
	}

	if inst.Placement == nil || inst.Placement.Tenancy != ec2types.TenancyDefault {
		t.Errorf("placement tenancy = %+v, want default", inst.Placement)
	}

	if inst.Monitoring == nil || inst.Monitoring.State != ec2types.MonitoringStateDisabled {
		t.Errorf("monitoring state = %+v, want disabled", inst.Monitoring)
	}

	if !strings.HasPrefix(aws.ToString(inst.PrivateDnsName), "ip-") {
		t.Errorf("privateDnsName = %q, want ip-...", aws.ToString(inst.PrivateDnsName))
	}

	if len(inst.NetworkInterfaces) != 1 {
		t.Fatalf("networkInterfaces len = %d, want 1", len(inst.NetworkInterfaces))
	}

	if inst.NetworkInterfaces[0].Attachment == nil ||
		aws.ToInt32(inst.NetworkInterfaces[0].Attachment.DeviceIndex) != 0 {
		t.Errorf("primary ENI deviceIndex != 0: %+v", inst.NetworkInterfaces[0].Attachment)
	}
}

// TestDescribeInstancesGroupsReservation pins that all instances launched by one
// RunInstances call share a single reservation, and a second call produces a
// distinct reservation (real EC2 <reservationSet> grouping).
func TestDescribeInstancesGroupsReservation(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	batch, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(3),
		MaxCount:     aws.Int32(3),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	ids := make([]string, 0, 3)
	for i := range batch.Instances {
		ids = append(ids, aws.ToString(batch.Instances[i].InstanceId))
	}

	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if len(out.Reservations) != 1 {
		t.Fatalf("reservations = %d, want 1 shared reservation", len(out.Reservations))
	}

	if len(out.Reservations[0].Instances) != 3 {
		t.Fatalf("instances in reservation = %d, want 3", len(out.Reservations[0].Instances))
	}

	other := runOneInstance(t, c)
	all, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: append(ids, other),
	})
	if err != nil {
		t.Fatalf("DescribeInstances (all): %v", err)
	}

	if len(all.Reservations) != 2 {
		t.Fatalf("reservations = %d, want 2 distinct reservations", len(all.Reservations))
	}
}

// TestDescribeInstancesPaginates pins that DescribeInstances honors MaxResults
// and NextToken, walking the reservation set one page at a time.
func TestDescribeInstancesPaginates(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	const reservations = 3
	for i := 0; i < reservations; i++ {
		runOneInstance(t, c)
	}

	seen := 0
	pages := 0

	var token *string

	for {
		out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(5),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeInstances page %d: %v", pages, err)
		}

		pages++
		for range out.Reservations {
			seen++
		}

		if aws.ToString(out.NextToken) == "" {
			break
		}

		token = out.NextToken

		if pages > reservations+1 {
			t.Fatal("pagination did not terminate")
		}
	}

	// MaxResults=5 with 3 reservations fits on one page (no token).
	if seen != reservations {
		t.Fatalf("saw %d reservations across pages, want %d", seen, reservations)
	}
}

// TestDescribeInstancesPaginatesOnePerPage pins the multi-page path: MaxResults=1
// yields one reservation per page with a NextToken until exhausted.
func TestDescribeInstancesPaginatesOnePerPage(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	const reservations = 3
	for i := 0; i < reservations; i++ {
		runOneInstance(t, c)
	}

	seen := 0
	pages := 0

	var token *string

	for {
		out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(1),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeInstances page %d: %v", pages, err)
		}

		pages++
		seen += len(out.Reservations)

		if aws.ToString(out.NextToken) == "" {
			break
		}

		token = out.NextToken

		if pages > reservations+1 {
			t.Fatal("pagination did not terminate")
		}
	}

	if pages != reservations {
		t.Fatalf("pages = %d, want %d (one reservation per page)", pages, reservations)
	}

	if seen != reservations {
		t.Fatalf("saw %d reservations, want %d", seen, reservations)
	}
}

// TestRunInstancesAllocatesIPFromSubnet pins that an instance launched into a
// subnet draws its primary private IPv4 from that subnet's CIDR (real EC2), not
// the global 10.0.<n> pool.
func TestRunInstancesAllocatesIPFromSubnet(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	subnet, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     vpc.Vpc.VpcId,
		CidrBlock: aws.String("10.0.5.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	run, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		SubnetId:     subnet.Subnet.SubnetId,
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	ip := aws.ToString(run.Instances[0].PrivateIpAddress)
	if !strings.HasPrefix(ip, "10.0.5.") {
		t.Fatalf("private IP %q not allocated from subnet CIDR 10.0.5.0/24", ip)
	}
}

// TestDescribeInstanceReportsKeyNameAndGroupName pins that a DescribeInstances
// item echoes the launch key pair and resolves security-group names alongside
// ids (both read by SDK clients).
func TestDescribeInstanceReportsKeyNameAndGroupName(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	if _, err := c.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String("launch-key")}); err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web-sg"),
		Description: aws.String("test"),
		VpcId:       vpc.Vpc.VpcId,
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	run, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:          aws.String("ami-123"),
		InstanceType:     ec2types.InstanceTypeT2Micro,
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		KeyName:          aws.String("launch-key"),
		SecurityGroupIds: []string{aws.ToString(sg.GroupId)},
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)
	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	inst := out.Reservations[0].Instances[0]
	if aws.ToString(inst.KeyName) != "launch-key" {
		t.Errorf("keyName = %q, want launch-key", aws.ToString(inst.KeyName))
	}

	if len(inst.SecurityGroups) != 1 {
		t.Fatalf("securityGroups len = %d, want 1", len(inst.SecurityGroups))
	}

	if aws.ToString(inst.SecurityGroups[0].GroupName) != "web-sg" {
		t.Errorf("group name = %q, want web-sg", aws.ToString(inst.SecurityGroups[0].GroupName))
	}
}

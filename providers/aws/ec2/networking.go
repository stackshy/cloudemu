package ec2

import "context"

// Networking is the slice of the networking mock EC2 needs to keep an instance's
// primary (eth0) network interface in step with its lifecycle. Real EC2 creates
// an eth0 ENI in the instance's subnet at launch and deletes it on terminate;
// that interface is what makes DeleteSubnet / DeleteSecurityGroup refuse while
// the instance is running, so it has to be materialized rather than implied.
type Networking interface {
	CreatePrimaryNetworkInterface(ctx context.Context, instanceID, subnetID string, groups []string) error
	ReleaseInstanceNetworkInterfaces(ctx context.Context, instanceID string) error
	// DisassociateInstanceAddresses clears the association of any elastic IP
	// bound to the instance on terminate, leaving the address allocated but
	// unassociated — matching real EC2, which disassociates (does not release)
	// an instance's EIPs when it is terminated.
	DisassociateInstanceAddresses(ctx context.Context, instanceID string) error
	// SetPrimaryNetworkInterfaceSourceDestCheck mirrors
	// ModifyInstanceAttribute(SourceDestCheck) onto the instance's primary
	// (eth0) ENI. Real EC2 has exactly one source/dest-check flag, reported
	// both by DescribeInstanceAttribute and embedded in
	// DescribeInstances/DescribeNetworkInterfaces; without this the two
	// stores diverge and a NAT-instance-shaped Terraform config never
	// converges. A no-op when the instance has no primary ENI to update.
	SetPrimaryNetworkInterfaceSourceDestCheck(ctx context.Context, instanceID string, value bool) error
}

// SetNetworking wires the networking mock in. Without it an instance launches
// with no materialized primary ENI, so the VPC delete guards can't see it.
func (m *Mock) SetNetworking(n Networking) {
	m.networking = n
}

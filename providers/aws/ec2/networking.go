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
}

// SetNetworking wires the networking mock in. Without it an instance launches
// with no materialized primary ENI, so the VPC delete guards can't see it.
func (m *Mock) SetNetworking(n Networking) {
	m.networking = n
}

package compute

import (
	"context"
	"strings"

	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// tagSecurityLists is the key server/oci/vcn writes a subnet's security list
// OCIDs under. An instance's effective rules are the union of its VNIC's NSGs
// and its subnet's security lists, so the launch has to read it; the constant
// is mirrored rather than imported because the handler package sits above this
// one. server/oci/compute's TestSubnetSecurityListsReachTheInstance pins it.
const tagSecurityLists = internalTagPrefix + "ociSecurityListIds"

// Networking is the slice of the VCN mock Compute needs to place an instance:
// launching one creates a VNIC in a subnet rather than modeling an interface
// of its own. *providers/oci/vcn.Mock satisfies it.
type Networking interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
	CreateNetworkInterface(
		ctx context.Context, subnetID, description string, tags map[string]string,
	) (*netdriver.NetworkInterface, error)
	DeleteNetworkInterface(ctx context.Context, id string) error
	DescribeVNICs(ctx context.Context, ids []string) ([]vcnprovider.VNIC, error)
	UpdateVNIC(
		ctx context.Context, id string, name, hostname *string, nsgIDs []string,
	) (*vcnprovider.VNIC, error)
	Defaults(vcnID string) vcnprovider.DefaultResources
}

// SetNetworking wires the VCN mock in. Without it an instance launched into a
// subnet still records the subnet, but gets no VNIC, no VCN and a synthetic
// private address.
func (m *Mock) SetNetworking(n Networking) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.net = n
}

// placement is where an instance's VNIC landed.
type placement struct {
	VNICID          string
	AttachmentID    string
	SubnetID        string
	VCNID           string
	PrivateIP       string
	PublicIP        string
	SecurityListIDs []string
}

// place creates the instance's primary VNIC in its subnet and reports where it
// landed. It runs without m.mu held: the VCN mock has a lock of its own and
// never calls back into Compute.
func (m *Mock) place(ctx context.Context, net Networking, subnetID, displayName, hostname string,
	nsgIDs []string, tags map[string]string,
) (placement, error) {
	out := placement{SubnetID: subnetID}

	if subnetID == "" || net == nil {
		out.PrivateIP = m.nextIP()

		return out, nil
	}

	subnets, err := net.DescribeSubnets(ctx, []string{subnetID})
	if err != nil {
		return out, err
	}

	if len(subnets) == 0 {
		return out, notFoundf("subnet %q not found", subnetID)
	}

	out.VCNID = subnets[0].VPCID
	out.SecurityListIDs = securityListsOf(net, &subnets[0])

	nic, err := net.CreateNetworkInterface(ctx, subnetID, displayName, tags)
	if err != nil {
		return out, err
	}

	out.VNICID = nic.ID
	out.AttachmentID = nic.AttachmentID

	if len(nsgIDs) > 0 || hostname != "" {
		if _, err := net.UpdateVNIC(ctx, nic.ID, nil, hostnamePtr(hostname), nsgIDs); err != nil {
			return out, err
		}
	}

	out.PrivateIP, out.PublicIP = addressesOf(ctx, net, nic.ID)
	if out.PrivateIP == "" {
		out.PrivateIP = m.nextIP()
	}

	return out, nil
}

// addressesOf reads the addresses the VCN service gave a VNIC. A read failure
// leaves them empty, and the caller falls back to a synthetic address.
func addressesOf(ctx context.Context, net Networking, vnicID string) (privateIP, publicIP string) {
	vnics, err := net.DescribeVNICs(ctx, []string{vnicID})
	if err != nil || len(vnics) == 0 {
		return "", ""
	}

	return vnics[0].PrivateIP, vnics[0].PublicIP
}

// unplace deletes an instance's VNIC. A failure is not fatal to the terminate:
// the instance is gone either way, and a dangling VNIC is what real OCI leaves
// behind when a detach races a delete.
func unplace(ctx context.Context, net Networking, vnicID string) {
	if net == nil || vnicID == "" {
		return
	}

	_ = net.DeleteNetworkInterface(ctx, vnicID)
}

// securityListsOf returns the security lists governing a subnet: the ones the
// subnet was created with, or its VCN's default list.
func securityListsOf(net Networking, subnet *netdriver.SubnetInfo) []string {
	if listed := subnet.Tags[tagSecurityLists]; listed != "" {
		out := make([]string, 0, 1)

		for _, id := range strings.Split(listed, ",") {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, id)
			}
		}

		if len(out) > 0 {
			return out
		}
	}

	if def := net.Defaults(subnet.VPCID).SecurityListID; def != "" {
		return []string{def}
	}

	return nil
}

// networking returns the wired VCN mock under the read lock, so a caller can
// reach it without holding m.mu across another service's call.
func (m *Mock) networking() Networking {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.net
}

func hostnamePtr(hostname string) *string {
	if hostname == "" {
		return nil
	}

	return &hostname
}

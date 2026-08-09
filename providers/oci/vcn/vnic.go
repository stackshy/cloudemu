package vcn

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const typeVNICAttachment = "vnicattachment"

// firstHostOffset is the first address a VNIC can take in a subnet: OCI
// reserves the network address and the virtual router below it. The broadcast
// address at the top is reserved too, so allocation runs between the two.
const firstHostOffset = 2

type vnicData struct {
	ID           string
	VCNID        string
	SubnetID     string
	Status       string
	AttachmentID string
	Name         string
	Hostname     string
	MacAddress   string
	IsPrimary    bool
	NSGIDs       []string
	Tags         map[string]string
}

type privateIPData struct {
	ID        string
	SubnetID  string
	VNICID    string
	Address   string
	Name      string
	Hostname  string
	IsPrimary bool
}

// VNIC is OCI's view of a network interface. The portable projection carries
// no address, MAC or security group membership, so the handler reads this.
type VNIC struct {
	ID         string
	VCNID      string
	SubnetID   string
	Name       string
	Hostname   string
	MacAddress string
	IsPrimary  bool
	NSGIDs     []string
	PrivateIP  string
	PublicIP   string
	State      string
}

// PrivateIP is OCI's private IP resource, an address on a VNIC.
type PrivateIP struct {
	ID        string
	SubnetID  string
	VNICID    string
	Address   string
	Name      string
	Hostname  string
	IsPrimary bool
}

// CreateNetworkInterface creates a VNIC in a subnet, along with the primary
// private IP OCI gives every VNIC.
func (m *Mock) CreateNetworkInterface(
	_ context.Context, subnetID, description string, tags map[string]string,
) (*driver.NetworkInterface, error) {
	s, ok := m.subnets.Get(subnetID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet %q not found", subnetID)
	}

	id := m.newOCID(typeVNIC)

	if _, err := m.addPrivateIP(s, id, "", "", "", true); err != nil {
		return nil, err
	}

	v := &vnicData{
		ID:           id,
		VCNID:        s.VCNID,
		SubnetID:     subnetID,
		Status:       StateAvailable,
		AttachmentID: m.newOCID(typeVNICAttachment),
		Name:         description,
		MacAddress:   mockMAC(id),
		IsPrimary:    true,
		Tags:         copyTags(tags),
	}

	m.vnics.Set(id, v)
	m.record(id)

	info := toVNICInterface(v)

	return &info, nil
}

// DescribeNetworkInterfaces returns VNICs matching the given OCIDs, or all if
// empty.
func (m *Mock) DescribeNetworkInterfaces(_ context.Context, ids []string) ([]driver.NetworkInterface, error) {
	return describeResources(m.vnics, ids, toVNICInterface), nil
}

// DetachNetworkInterface detaches a VNIC from the instance holding it.
func (m *Mock) DetachNetworkInterface(_ context.Context, attachmentID string, force bool) error {
	for _, held := range m.vnics.All() {
		if held.AttachmentID != attachmentID {
			continue
		}

		return mutate(m.vnics, held.ID, vnicNotFound(held.ID), func(v *vnicData) error {
			if v.IsPrimary && !force {
				return cerrors.Newf(cerrors.FailedPrecondition,
					"VNIC attachment %q is primary; detach requires force", attachmentID)
			}

			v.AttachmentID = ""
			v.Status = StateDetached

			return nil
		})
	}

	return cerrors.Newf(cerrors.NotFound, "VNIC attachment %q not found", attachmentID)
}

// DeleteNetworkInterface deletes a VNIC and the private IPs on it.
func (m *Mock) DeleteNetworkInterface(_ context.Context, id string) error {
	if !m.vnics.Has(id) {
		return vnicNotFound(id)
	}

	for _, p := range m.privateIPs.All() {
		if p.VNICID == id {
			m.privateIPs.Delete(p.ID)
			m.forget(p.ID)
		}
	}

	m.vnics.Delete(id)
	m.forget(id)

	return nil
}

// DescribeVNICs returns the OCI view of VNICs matching the given OCIDs, or
// all if empty.
func (m *Mock) DescribeVNICs(_ context.Context, ids []string) ([]VNIC, error) {
	return describeResources(m.vnics, ids, m.toVNIC), nil
}

// UpdateVNIC changes a VNIC's display name, hostname and NSG membership. A nil
// pointer leaves that field alone.
func (m *Mock) UpdateVNIC(_ context.Context, id string, name, hostname *string, nsgIDs []string) (*VNIC, error) {
	var out VNIC

	err := mutate(m.vnics, id, vnicNotFound(id), func(v *vnicData) error {
		// Membership is checked before anything is written, so a bad NSG
		// leaves the VNIC as it was rather than half-updated.
		for _, nsgID := range nsgIDs {
			if !m.nsgs.Has(nsgID) {
				return nsgNotFound(nsgID)
			}
		}

		if name != nil {
			v.Name = *name
		}

		if hostname != nil {
			v.Hostname = *hostname
		}

		if nsgIDs != nil {
			v.NSGIDs = copyStringSlice(nsgIDs)
		}

		out = m.toVNIC(v)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// VNICsInNSG returns the VNICs that are members of a network security group.
func (m *Mock) VNICsInNSG(_ context.Context, nsgID string) ([]VNIC, error) {
	if !m.nsgs.Has(nsgID) {
		return nil, nsgNotFound(nsgID)
	}

	out := make([]VNIC, 0)

	for _, v := range m.vnics.SortedValues() {
		for _, member := range v.NSGIDs {
			if member == nsgID {
				out = append(out, m.toVNIC(v))
				break
			}
		}
	}

	return out, nil
}

// CreatePrivateIP adds a secondary private IP to a VNIC.
func (m *Mock) CreatePrivateIP(_ context.Context, vnicID, address, name, hostname string) (*PrivateIP, error) {
	v, ok := m.vnics.Get(vnicID)
	if !ok {
		return nil, vnicNotFound(vnicID)
	}

	s, ok := m.subnets.Get(v.SubnetID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet %q not found", v.SubnetID)
	}

	if address != "" {
		for _, p := range m.privateIPs.All() {
			if p.SubnetID == s.ID && p.Address == address {
				return nil, cerrors.Newf(cerrors.AlreadyExists, "private IP %q is already in use", address)
			}
		}
	}

	return m.addPrivateIP(s, vnicID, address, name, hostname, false)
}

// addPrivateIP stores a private IP, allocating the lowest free address in the
// subnet when the caller names none.
func (m *Mock) addPrivateIP(
	s *subnetData, vnicID, address, name, hostname string, isPrimary bool,
) (*PrivateIP, error) {
	if address == "" {
		free, err := m.freeAddress(s)
		if err != nil {
			return nil, err
		}

		address = free
	}

	id := m.newOCID(typePrivateIP)
	p := &privateIPData{
		ID:        id,
		SubnetID:  s.ID,
		VNICID:    vnicID,
		Address:   address,
		Name:      name,
		Hostname:  hostname,
		IsPrimary: isPrimary,
	}

	m.privateIPs.Set(id, p)
	m.record(id)

	info := toPrivateIPInfo(p)

	return &info, nil
}

// freeAddress returns the lowest address in the subnet no private IP holds.
// Counting the live ones would re-mint an address already handed out once an
// earlier one below it is deleted.
func (m *Mock) freeAddress(s *subnetData) (string, error) {
	used := make(map[string]struct{})

	for _, p := range m.privateIPs.All() {
		if p.SubnetID == s.ID {
			used[p.Address] = struct{}{}
		}
	}

	last := lastHostOffset(s.CIDRBlock)

	for offset := uint32(firstHostOffset); offset <= last; offset++ {
		address := hostIP(s.CIDRBlock, offset)
		if _, taken := used[address]; !taken {
			return address, nil
		}
	}

	return "", cerrors.Newf(cerrors.ResourceExhausted, "subnet %q has no free addresses", s.ID)
}

// lastHostOffset is the highest offset a subnet can hand out. It is zero for a
// block that is not IPv4 or is too small to host an address.
func lastHostOffset(cidr string) uint32 {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || len(ipnet.Mask) != net.IPv4len {
		return 0
	}

	// The complement of the mask is the offset of the broadcast address, and
	// the last address a VNIC can take sits just below it.
	broadcast := ^binary.BigEndian.Uint32(ipnet.Mask)
	if broadcast == 0 {
		return 0
	}

	return broadcast - 1
}

// DeletePrivateIP deletes a secondary private IP. A VNIC's primary address
// only goes away with the VNIC.
func (m *Mock) DeletePrivateIP(_ context.Context, id string) error {
	p, ok := m.privateIPs.Get(id)
	if !ok {
		return privateIPNotFound(id)
	}

	if p.IsPrimary {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete primary private IP %q", id)
	}

	m.privateIPs.Delete(id)
	m.forget(id)

	return nil
}

// DescribePrivateIPs returns private IPs matching the given OCIDs, or all if
// empty.
func (m *Mock) DescribePrivateIPs(_ context.Context, ids []string) ([]PrivateIP, error) {
	return describeResources(m.privateIPs, ids, toPrivateIPInfo), nil
}

// UpdatePrivateIP changes a private IP's display name and hostname.
func (m *Mock) UpdatePrivateIP(_ context.Context, id string, name, hostname *string) (*PrivateIP, error) {
	var out PrivateIP

	err := mutate(m.privateIPs, id, privateIPNotFound(id), func(p *privateIPData) error {
		if name != nil {
			p.Name = *name
		}

		if hostname != nil {
			p.Hostname = *hostname
		}

		out = toPrivateIPInfo(p)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

func vnicNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "VNIC %q not found", id)
}

func privateIPNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "private IP %q not found", id)
}

func (m *Mock) toVNIC(v *vnicData) VNIC {
	out := VNIC{
		ID:         v.ID,
		VCNID:      v.VCNID,
		SubnetID:   v.SubnetID,
		Name:       v.Name,
		Hostname:   v.Hostname,
		MacAddress: v.MacAddress,
		IsPrimary:  v.IsPrimary,
		NSGIDs:     copyStringSlice(v.NSGIDs),
		State:      v.Status,
	}

	for _, p := range m.privateIPs.SortedValues() {
		if p.VNICID != v.ID || !p.IsPrimary {
			continue
		}

		out.PrivateIP = p.Address
		out.PublicIP = m.publicIPFor(p.ID)

		break
	}

	return out
}

// publicIPFor returns the address assigned to a private IP, if any.
func (m *Mock) publicIPFor(privateIPID string) string {
	for _, ip := range m.publicIPs.All() {
		if ip.AssignedTo == privateIPID {
			return ip.Address
		}
	}

	return ""
}

func toVNICInterface(v *vnicData) driver.NetworkInterface {
	return driver.NetworkInterface{
		ID:           v.ID,
		VPCID:        v.VCNID,
		SubnetID:     v.SubnetID,
		Status:       v.Status,
		AttachmentID: v.AttachmentID,
		Description:  v.Name,
		Tags:         copyTags(v.Tags),
	}
}

func toPrivateIPInfo(p *privateIPData) PrivateIP {
	return PrivateIP{
		ID:        p.ID,
		SubnetID:  p.SubnetID,
		VNICID:    p.VNICID,
		Address:   p.Address,
		Name:      p.Name,
		Hostname:  p.Hostname,
		IsPrimary: p.IsPrimary,
	}
}

// mockMAC derives a stable MAC address from an OCID, in the prefix OCI hands
// out to VNICs.
func mockMAC(id string) string {
	sum := sha256.Sum256([]byte(id))

	return fmt.Sprintf("00:00:17:%02x:%02x:%02x", sum[0], sum[1], sum[2])
}

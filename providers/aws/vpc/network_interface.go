package vpc

import (
	"context"
	"fmt"
	"net"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Network interface status values.
const (
	ENIStatusAvailable = "available"
	ENIStatusInUse     = "in-use"

	// eniFirstHostOffset is the offset of the first ENI-assignable host address
	// inside a subnet CIDR. EC2 reserves the first four addresses (.0-.3) of
	// every subnet, so the first usable host is at offset 4.
	eniFirstHostOffset = 4

	// eniMACPrefix is the OUI ENI MACs carry; 0a is a locally-administered
	// address, matching the shape real EC2 hands back.
	eniMACPrefix = "0a"
	// macByteCount is the number of hashed bytes after the fixed OUI prefix,
	// macHashPrime seeds the rolling hash, and bitsPerByte splits the hash into
	// address octets.
	macByteCount = 5
	macHashPrime = 131
	bitsPerByte  = 8

	// eniInUseErrPrefix marks the delete-while-attached precondition so the wire
	// layer can emit InvalidNetworkInterface.InUse rather than the generic
	// DependencyViolation.
	eniInUseErrPrefix = "InvalidNetworkInterface.InUse: "

	// primaryDeviceIndex is the device index EC2 gives an instance's primary
	// (eth0) network interface — always 0.
	primaryDeviceIndex = 0

	// primaryENIDescription is the description real EC2 stamps on the eth0 ENI
	// it auto-creates for a launched instance.
	primaryENIDescription = "Primary network interface"
)

type eniData struct {
	ID              string
	VPCID           string
	SubnetID        string
	Status          string
	AttachmentID    string
	InstanceID      string
	DeviceIndex     int
	Description     string
	PrivateIP       string
	MacAddress      string
	SourceDestCheck bool
	SecurityGroups  []string
	Tags            map[string]string
	// deleteOnTermination marks an ENI EC2 created as an instance's primary
	// (eth0) interface at launch. Real EC2 deletes exactly these on
	// TerminateInstances and leaves secondary/standalone ENIs in place, so the
	// terminate-time release keys on this flag rather than the attachment alone.
	deleteOnTermination bool
}

// CreateNetworkInterface creates a standalone, unattached ENI in the given
// subnet (ec2:CreateNetworkInterface). The VPC is resolved from the subnet, so
// an unknown subnet is NotFound. groups records the security groups the ENI is
// in, so DeleteSecurityGroup can refuse to delete one still attached.
func (m *Mock) CreateNetworkInterface(
	_ context.Context, subnetID, description string, groups []string, tags map[string]string,
) (*driver.NetworkInterface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subnets.Get(subnetID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "InvalidSubnetID.NotFound: subnet %q not found", subnetID)
	}

	id := idgen.GenerateID("eni-")
	eni := &eniData{
		ID:       id,
		VPCID:    sub.VPCID,
		SubnetID: subnetID,
		Status:   ENIStatusAvailable,
		// Real EC2 auto-assigns a private IPv4 from the subnet, a MAC address,
		// and defaults source/dest check on. IaC tools (aws_network_interface)
		// read all three straight off the create/describe response.
		PrivateIP:       m.allocateENIPrivateIP(subnetID, sub.CIDRBlock),
		MacAddress:      mockMAC(id),
		SourceDestCheck: true,
		Description:     description,
		SecurityGroups:  append([]string(nil), groups...),
		Tags:            copyTags(tags),
	}
	m.enis.Set(id, eni)

	info := toENIInfo(eni)

	return &info, nil
}

// CreatePrimaryNetworkInterface materializes the eth0 interface real EC2 creates
// for an instance at launch: an in-use ENI in the instance's subnet, carrying
// the instance's security groups, at device index 0, attached to the instance
// and flagged delete-on-termination. Its existence is what makes DeleteSubnet
// and DeleteSecurityGroup refuse while the instance is running, matching AWS.
//
// A launch with no subnet materializes nothing (there is no subnet to place the
// interface in). An unknown subnet is NotFound, so a bad launch surfaces rather
// than silently skipping the interface.
func (m *Mock) CreatePrimaryNetworkInterface(_ context.Context, instanceID, subnetID string, groups []string) error {
	if subnetID == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subnets.Get(subnetID)
	if !ok {
		return errors.Newf(errors.NotFound, "InvalidSubnetID.NotFound: subnet %q not found", subnetID)
	}

	id := idgen.GenerateID("eni-")
	eni := &eniData{
		ID:                  id,
		VPCID:               sub.VPCID,
		SubnetID:            subnetID,
		Status:              ENIStatusInUse,
		AttachmentID:        idgen.GenerateID("eni-attach-"),
		InstanceID:          instanceID,
		DeviceIndex:         primaryDeviceIndex,
		Description:         primaryENIDescription,
		PrivateIP:           m.allocateENIPrivateIP(subnetID, sub.CIDRBlock),
		MacAddress:          mockMAC(id),
		SourceDestCheck:     true,
		SecurityGroups:      append([]string(nil), groups...),
		deleteOnTermination: true,
	}
	m.enis.Set(id, eni)

	return nil
}

// ReleaseInstanceNetworkInterfaces reconciles an instance's ENIs on terminate,
// matching real EC2's delete-on-termination default per interface:
//
//   - delete-on-termination interfaces (the primary eth0 that
//     CreatePrimaryNetworkInterface made) are deleted; releasing them is what
//     lets a subsequent DeleteSubnet / DeleteSecurityGroup succeed.
//   - other attached interfaces (secondary ENIs the user attached with
//     AttachNetworkInterface, deleteOnTermination false) are detached back to
//     `available` — not deleted — so they survive the instance and can be
//     reattached, and no longer wedge a DeleteSubnet on a dead instance.
//
// Standalone/managed interfaces not attached to this instance are untouched.
func (m *Mock) ReleaseInstanceNetworkInterfaces(_ context.Context, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, eni := range m.enis.All() {
		if eni.InstanceID != instanceID {
			continue
		}

		if eni.deleteOnTermination {
			m.enis.Delete(id)

			continue
		}

		eni.AttachmentID = ""
		eni.InstanceID = ""
		eni.DeviceIndex = 0
		eni.Status = ENIStatusAvailable
	}

	return nil
}

// DescribeNetworkInterfaces returns ENIs matching the given IDs, or all if empty.
//
// An explicitly named ID that does not exist is NotFound rather than an empty
// list: a caller draining a VPC reads an empty answer as "already gone" and
// moves on, which is the wrong conclusion when it asked about one specific
// interface.
func (m *Mock) DescribeNetworkInterfaces(_ context.Context, ids []string) ([]driver.NetworkInterface, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range ids {
		if !m.enis.Has(id) {
			return nil, errors.Newf(errors.NotFound,
				"InvalidNetworkInterfaceID.NotFound: network interface %q not found", id)
		}
	}

	return describeResources(m.enis, ids, toENIInfo), nil
}

// AttachNetworkInterface attaches an available ENI to an instance and returns
// the new attachment id (ec2:AttachNetworkInterface). An interface already in
// use cannot be re-attached; real EC2 answers InvalidNetworkInterface.InUse.
func (m *Mock) AttachNetworkInterface(
	_ context.Context, networkInterfaceID, instanceID string, deviceIndex int,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	eni, ok := m.enis.Get(networkInterfaceID)
	if !ok {
		return "", errors.Newf(errors.NotFound, "network interface %q not found", networkInterfaceID)
	}

	if eni.AttachmentID != "" {
		return "", errors.Newf(errors.FailedPrecondition,
			"InvalidNetworkInterface.InUse: network interface %q is already attached to instance %q",
			networkInterfaceID, eni.InstanceID)
	}

	attachmentID := idgen.GenerateID("eni-attach-")
	eni.AttachmentID = attachmentID
	eni.InstanceID = instanceID
	eni.DeviceIndex = deviceIndex
	eni.Status = ENIStatusInUse

	return attachmentID, nil
}

// DetachNetworkInterface detaches the ENI carrying the given attachment ID.
//
// force is accepted and ignored: the emulator has no in-flight traffic for a
// forced detach to interrupt, so the distinction has no observable effect.
func (m *Mock) DetachNetworkInterface(_ context.Context, attachmentID string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, eni := range m.enis.All() {
		if eni.AttachmentID != attachmentID {
			continue
		}

		eni.AttachmentID = ""
		eni.InstanceID = ""
		eni.DeviceIndex = 0
		eni.Status = ENIStatusAvailable

		return nil
	}

	return errors.Newf(errors.NotFound, "network interface attachment %q not found", attachmentID)
}

// DeleteNetworkInterface deletes the ENI with the given ID.
//
// An attached ENI cannot be deleted — real EC2 answers DependencyViolation,
// and callers rely on that to know a drain is still in progress rather than
// complete.
func (m *Mock) DeleteNetworkInterface(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eni, ok := m.enis.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "network interface %q not found", id)
	}

	if eni.AttachmentID != "" {
		return errors.Newf(errors.FailedPrecondition,
			"%snetwork interface %q is currently in use and cannot be deleted", eniInUseErrPrefix, id)
	}

	m.enis.Delete(id)

	return nil
}

// ModifyNetworkInterfaceAttribute changes one or more ENI attributes
// (ec2:ModifyNetworkInterfaceAttribute). A nil field in update leaves that
// attribute untouched, matching an API that accepts one attribute per call.
// SourceDestCheck=false is the required step for a NAT-instance / firewall /
// router VM; Groups swaps the interface's security groups; Description renames it.
func (m *Mock) ModifyNetworkInterfaceAttribute(
	_ context.Context, id string, update driver.NetworkInterfaceAttributeUpdate,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eni, ok := m.enis.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "network interface %q not found", id)
	}

	if update.SourceDestCheck != nil {
		eni.SourceDestCheck = *update.SourceDestCheck
	}

	if update.Description != nil {
		eni.Description = *update.Description
	}

	if update.Groups != nil {
		eni.SecurityGroups = append([]string(nil), update.Groups...)
	}

	return nil
}

// allocateENIPrivateIP hands out the next private IPv4 inside the subnet's CIDR,
// skipping the four addresses EC2 reserves at the front of every subnet. The
// caller already holds m.mu. An unparseable CIDR yields an empty string rather
// than a bogus address.
func (m *Mock) allocateENIPrivateIP(subnetID, cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	base := ipNet.IP.To4()
	if base == nil {
		return ""
	}

	if m.eniIPCounters == nil {
		m.eniIPCounters = map[string]int{}
	}

	offset := m.eniIPCounters[subnetID] + eniFirstHostOffset
	m.eniIPCounters[subnetID]++

	host := make(net.IP, len(base))
	copy(host, base)

	carry := offset
	for i := len(host) - 1; i >= 0 && carry > 0; i-- {
		sum := int(host[i]) + carry
		host[i] = byte(sum % maxOctetValue) //nolint:gosec // sum%256 is always in [0,255]
		carry = sum / maxOctetValue
	}

	if !ipNet.Contains(host) {
		return ""
	}

	return host.String()
}

// mockMAC derives a stable locally-administered MAC address from an ENI id, so
// the same interface always reports the same address across describes.
func mockMAC(seed string) string {
	var sum uint64
	for _, c := range []byte(seed) {
		sum = sum*macHashPrime + uint64(c)
	}

	b := make([]byte, macByteCount)
	for i := range b {
		b[i] = byte(sum >> (uint(i) * bitsPerByte)) //nolint:gosec // byte() intentionally truncates to one octet
	}

	return fmt.Sprintf("%s:%02x:%02x:%02x:%02x:%02x", eniMACPrefix, b[0], b[1], b[2], b[3], b[4])
}

// attachManagedENI records the interface a managed resource (NAT gateway,
// load balancer, managed database) occupies while it exists. Callers deleting
// a VPC must drain these before the VPC delete is accepted, so the emulator
// has to actually hold one rather than report an empty set.
func (m *Mock) attachManagedENI(vpcID, subnetID, description string) *eniData {
	id := idgen.GenerateID("eni-")

	eni := &eniData{
		ID:           id,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		Status:       ENIStatusInUse,
		AttachmentID: idgen.GenerateID("eni-attach-"),
		Description:  description,
	}
	m.enis.Set(id, eni)

	return eni
}

// releaseManagedENIs drops the interfaces held by a managed resource. Real AWS
// releases them asynchronously, which is the race the caller's drain loop
// exists to absorb; the emulator releases synchronously so teardown is
// deterministic.
func (m *Mock) releaseManagedENIs(description string) {
	for id, eni := range m.enis.All() {
		if eni.Description == description {
			m.enis.Delete(id)
		}
	}
}

func toENIInfo(e *eniData) driver.NetworkInterface {
	return driver.NetworkInterface{
		ID:              e.ID,
		VPCID:           e.VPCID,
		SubnetID:        e.SubnetID,
		Status:          e.Status,
		AttachmentID:    e.AttachmentID,
		InstanceID:      e.InstanceID,
		DeviceIndex:     e.DeviceIndex,
		Description:     e.Description,
		PrivateIP:       e.PrivateIP,
		MacAddress:      e.MacAddress,
		SourceDestCheck: e.SourceDestCheck,
		Tags:            copyTags(e.Tags),
	}
}

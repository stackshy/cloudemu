package globalaccelerator

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// CreateAccelerator provisions a new accelerator synchronously with stable
// computed fields (arn, two deterministic static IPv4 addresses, dnsName,
// dualStackDnsName, status, createdTime) minted once and stored. Status settles
// to DEPLOYED immediately. A default (flow-logs-disabled) attributes record is
// created alongside it.
func (m *Mock) CreateAccelerator(_ context.Context, in *driver.CreateAcceleratorInput) (*driver.Accelerator, error) {
	if in.Name == "" {
		return nil, invalidArgument("Name is required")
	}

	ipType := in.IPAddressType
	if ipType == "" {
		ipType = ipAddressTypeIPv4
	}

	if ipType != ipAddressTypeIPv4 && ipType != ipAddressTypeDualStack {
		return nil, invalidArgument("IpAddressType must be IPV4 or DUAL_STACK, got %q", ipType)
	}

	id := idgen.UUID()
	arn := m.acceleratorARN(id)
	now := m.now()

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	a := driver.Accelerator{
		AcceleratorArn:   arn,
		Name:             in.Name,
		IPAddressType:    ipType,
		Enabled:          enabled,
		IPSets:           buildIPSets(id, ipType),
		DNSName:          dnsLabel(id) + ".awsglobalaccelerator.com",
		DualStackDNSName: dnsLabel(id) + ".dualstack.awsglobalaccelerator.com",
		Status:           statusDeployed,
		CreatedTime:      now,
		LastModifiedTime: now,
		Tags:             copyTags(in.Tags),
	}

	m.accelerators.Set(arn, a)
	m.attributes.Set(arn, driver.AcceleratorAttributes{})

	out := copyAccelerator(&a)

	return &out, nil
}

// buildIPSets mints the static IP sets for an accelerator. An IPV4 accelerator
// gets one IPv4 set of two deterministic addresses; a DUAL_STACK accelerator adds
// an IPv6 set.
func buildIPSets(id, ipType string) []driver.IPSet {
	sets := []driver.IPSet{{
		IPFamily:        ipFamilyIPv4,
		IPAddressFamily: ipFamilyIPv4,
		IPAddresses:     staticIPv4Addresses(id),
	}}

	if ipType == ipAddressTypeDualStack {
		sets = append(sets, driver.IPSet{
			IPFamily:        ipFamilyIPv6,
			IPAddressFamily: ipFamilyIPv6,
			IPAddresses:     []string{"2600:9000:" + dnsLabel(id)[1:5] + "::1"},
		})
	}

	return sets
}

// DescribeAccelerator returns the accelerator by ARN, or an
// AcceleratorNotFoundException.
func (m *Mock) DescribeAccelerator(_ context.Context, arn string) (*driver.Accelerator, error) {
	a, ok := m.accelerators.Get(arn)
	if !ok {
		return nil, acceleratorNotFound(arn)
	}

	out := copyAccelerator(&a)

	return &out, nil
}

// UpdateAccelerator replaces the members present in the request and advances
// lastModifiedTime. The arn, ipSets, dnsName and createdTime stay stable; a
// change to DUAL_STACK adds the IPv6 set.
func (m *Mock) UpdateAccelerator(_ context.Context, in *driver.UpdateAcceleratorInput) (*driver.Accelerator, error) {
	a, ok := m.accelerators.Get(in.AcceleratorArn)
	if !ok {
		return nil, acceleratorNotFound(in.AcceleratorArn)
	}

	if in.Name != nil {
		a.Name = *in.Name
	}

	if in.IPAddressType != nil {
		if *in.IPAddressType != ipAddressTypeIPv4 && *in.IPAddressType != ipAddressTypeDualStack {
			return nil, invalidArgument("IpAddressType must be IPV4 or DUAL_STACK, got %q", *in.IPAddressType)
		}

		a.IPAddressType = *in.IPAddressType
		a.IPSets = adjustIPSets(a.IPSets, idFromAcceleratorArn(a.AcceleratorArn), *in.IPAddressType)
	}

	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}

	a.LastModifiedTime = m.now()

	m.accelerators.Set(in.AcceleratorArn, a)

	out := copyAccelerator(&a)

	return &out, nil
}

// adjustIPSets keeps the stable IPv4 set and adds or drops the IPv6 set to match
// the requested address type, so the two static IPv4 addresses never change.
func adjustIPSets(cur []driver.IPSet, id, ipType string) []driver.IPSet {
	var ipv4 []driver.IPSet

	if len(cur) > 0 {
		ipv4 = cur[:1]
	} else {
		ipv4 = buildIPSets(id, ipAddressTypeIPv4)
	}

	if ipType == ipAddressTypeDualStack {
		return append(copyIPSets(ipv4), buildIPSets(id, ipAddressTypeDualStack)[1])
	}

	return copyIPSets(ipv4)
}

// idFromAcceleratorArn extracts the accelerator id from its ARN resource part
// "accelerator/<id>".
func idFromAcceleratorArn(arn string) string {
	res := arnResource(arn)

	const kv = 2
	parts := strings.SplitN(res, "/", kv)

	if len(parts) < kv {
		return ""
	}

	return parts[1]
}

// DeleteAccelerator removes an accelerator. It is rejected while the accelerator
// is enabled (AcceleratorNotDisabledException) or still owns listeners
// (AssociatedListenerFoundException), mirroring the real service.
func (m *Mock) DeleteAccelerator(_ context.Context, arn string) error {
	a, ok := m.accelerators.Get(arn)
	if !ok {
		return acceleratorNotFound(arn)
	}

	if a.Enabled {
		return acceleratorNotDisabled(arn)
	}

	if m.acceleratorHasListeners(arn) {
		return associatedListenerFound(arn)
	}

	m.accelerators.Delete(arn)
	m.attributes.Delete(arn)

	return nil
}

// acceleratorHasListeners reports whether any listener belongs to the accelerator.
func (m *Mock) acceleratorHasListeners(acceleratorArn string) bool {
	return anyValue(m.listeners.SortedValues(), func(l *driver.Listener) bool {
		return l.AcceleratorArn == acceleratorArn
	})
}

// ListAccelerators returns a deterministic page of accelerators ordered by ARN.
func (m *Mock) ListAccelerators(_ context.Context, page driver.Page) ([]*driver.Accelerator, string, error) {
	out, next := paginateCopies(m.accelerators.SortedValues(), page, copyAccelerator)

	return out, next, nil
}

// DescribeAcceleratorAttributes returns the flow-log attributes of an accelerator.
func (m *Mock) DescribeAcceleratorAttributes(_ context.Context, arn string) (*driver.AcceleratorAttributes, error) {
	if !m.accelerators.Has(arn) {
		return nil, acceleratorNotFound(arn)
	}

	attr, ok := m.attributes.Get(arn)
	if !ok {
		attr = driver.AcceleratorAttributes{}
	}

	out := attr

	return &out, nil
}

// UpdateAcceleratorAttributes replaces the flow-log members present in the
// request and returns the resulting attributes.
func (m *Mock) UpdateAcceleratorAttributes(
	_ context.Context, in *driver.UpdateAcceleratorAttributesInput,
) (*driver.AcceleratorAttributes, error) {
	if !m.accelerators.Has(in.AcceleratorArn) {
		return nil, acceleratorNotFound(in.AcceleratorArn)
	}

	attr, ok := m.attributes.Get(in.AcceleratorArn)
	if !ok {
		attr = driver.AcceleratorAttributes{}
	}

	if in.FlowLogsEnabled != nil {
		attr.FlowLogsEnabled = *in.FlowLogsEnabled
	}

	if in.FlowLogsS3Bucket != nil {
		attr.FlowLogsS3Bucket = *in.FlowLogsS3Bucket
	}

	if in.FlowLogsS3Prefix != nil {
		attr.FlowLogsS3Prefix = *in.FlowLogsS3Prefix
	}

	m.attributes.Set(in.AcceleratorArn, attr)

	out := attr

	return &out, nil
}

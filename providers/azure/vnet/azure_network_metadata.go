package vnet

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock implements the optional Azure metadata surface.
var _ driver.AzureNetworkMetadata = (*Mock)(nil)

// PutAzureVNetMetadata stores the Azure-only virtual-network fields (region and
// full address-prefix list) for the VPC with the given driver id.
func (m *Mock) PutAzureVNetMetadata(_ context.Context, id string, meta driver.AzureVNetMetadata) error {
	m.azureVNetMeta.Set(id, cloneVNetMeta(meta))
	return nil
}

// GetAzureVNetMetadata returns the stored Azure virtual-network metadata for id.
func (m *Mock) GetAzureVNetMetadata(_ context.Context, id string) (driver.AzureVNetMetadata, bool) {
	meta, ok := m.azureVNetMeta.Get(id)
	if !ok {
		return driver.AzureVNetMetadata{}, false
	}

	return cloneVNetMeta(meta), true
}

// DeleteAzureVNetMetadata drops the stored metadata for id (called when the VPC
// is deleted).
func (m *Mock) DeleteAzureVNetMetadata(_ context.Context, id string) {
	m.azureVNetMeta.Delete(id)
}

// PutAzureNSGMetadata stores the Azure-only security-group fields (region and
// custom security rules) for the security group with the given driver id.
func (m *Mock) PutAzureNSGMetadata(_ context.Context, id string, meta driver.AzureNSGMetadata) error {
	m.azureNSGMeta.Set(id, cloneNSGMeta(meta))
	return nil
}

// GetAzureNSGMetadata returns the stored Azure security-group metadata for id.
func (m *Mock) GetAzureNSGMetadata(_ context.Context, id string) (driver.AzureNSGMetadata, bool) {
	meta, ok := m.azureNSGMeta.Get(id)
	if !ok {
		return driver.AzureNSGMetadata{}, false
	}

	return cloneNSGMeta(meta), true
}

// DeleteAzureNSGMetadata drops the stored metadata for id (called when the
// security group is deleted).
func (m *Mock) DeleteAzureNSGMetadata(_ context.Context, id string) {
	m.azureNSGMeta.Delete(id)
}

// cloneVNetMeta deep-copies the address-prefix slice so stored and returned
// values never alias a caller's slice.
func cloneVNetMeta(meta driver.AzureVNetMetadata) driver.AzureVNetMetadata {
	out := driver.AzureVNetMetadata{Location: meta.Location}
	if len(meta.AddressPrefixes) > 0 {
		out.AddressPrefixes = append([]string(nil), meta.AddressPrefixes...)
	}

	return out
}

// cloneNSGMeta deep-copies the rule slice so stored and returned values never
// alias a caller's slice.
func cloneNSGMeta(meta driver.AzureNSGMetadata) driver.AzureNSGMetadata {
	out := driver.AzureNSGMetadata{Location: meta.Location}
	if len(meta.SecurityRules) > 0 {
		out.SecurityRules = append([]driver.AzureNSGRule(nil), meta.SecurityRules...)
	}

	return out
}

package vnet

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock serves the Azure public-IP-prefix surface.
var _ driver.AzurePublicIPPrefixes = (*Mock)(nil)

const (
	// defaultPrefixLength is applied when a createOrUpdate omits prefixLength, so a
	// prefix always resolves to a concrete CIDR.
	defaultPrefixLength = 28
	// octetMask masks a byte out of the block counter when composing the CIDR.
	octetMask = 0xFF
)

// prefixKey composes the store key from the ARM addressing pair. Resource-group
// names are case-insensitive in Azure, so it is lower-cased; the prefix name is
// preserved as-is, mirroring asgKey.
func prefixKey(resourceGroup, name string) string {
	return strings.ToLower(resourceGroup) + "/" + name
}

// PutAzurePublicIPPrefix creates or replaces a prefix in place, keyed by
// (resourceGroup, name). On create it allocates a deterministic CIDR of the
// requested size; on an idempotent re-PUT it preserves the existing IPPrefix and
// PrefixLength (both immutable in real Azure) and only refreshes the mutable
// fields.
//
//nolint:gocritic // hugeParam: prefix mirrors the AzurePublicIPPrefixes driver signature.
func (m *Mock) PutAzurePublicIPPrefix(
	_ context.Context, prefix driver.AzurePublicIPPrefix,
) driver.AzurePublicIPPrefix {
	m.prefixMu.Lock()
	defer m.prefixMu.Unlock()

	key := prefixKey(prefix.ResourceGroup, prefix.Name)

	stored := clonePrefix(prefix)

	if existing, ok := m.azurePrefixes.Get(key); ok {
		// Re-PUT: the synthesized CIDR and its length are immutable, so carry them
		// forward rather than re-allocating a second block.
		stored.IPPrefix = existing.IPPrefix
		stored.PrefixLength = existing.PrefixLength
	} else {
		if stored.PrefixLength <= 0 {
			stored.PrefixLength = defaultPrefixLength
		}

		stored.IPPrefix = m.allocatePrefixCIDR(stored.PrefixLength)
	}

	m.azurePrefixes.Set(key, stored)

	return clonePrefix(stored)
}

// allocatePrefixCIDR hands out the next unused /24 from the 10.0.0.0/8 pool and
// masks it to prefixLength, giving each prefix a distinct, aligned CIDR. The
// counter is monotonic (deterministic, no randomness) and, since Azure IPv4
// public-IP prefixes are /24–/31, the host bits always fit inside the last octet
// so the .0 base is aligned. Caller holds prefixMu.
func (m *Mock) allocatePrefixCIDR(prefixLength int32) string {
	block := m.nextPrefixBlock
	m.nextPrefixBlock++

	return fmt.Sprintf("10.%d.%d.0/%d", (block>>8)&octetMask, block&octetMask, prefixLength)
}

// GetAzurePublicIPPrefix returns the prefix identified by (resourceGroup, name).
func (m *Mock) GetAzurePublicIPPrefix(
	_ context.Context, resourceGroup, name string,
) (driver.AzurePublicIPPrefix, bool) {
	prefix, ok := m.azurePrefixes.Get(prefixKey(resourceGroup, name))
	if !ok {
		return driver.AzurePublicIPPrefix{}, false
	}

	return clonePrefix(prefix), true
}

// DeleteAzurePublicIPPrefix removes the prefix, reporting whether it existed.
func (m *Mock) DeleteAzurePublicIPPrefix(_ context.Context, resourceGroup, name string) bool {
	return m.azurePrefixes.Delete(prefixKey(resourceGroup, name))
}

// ListAzurePublicIPPrefixes returns the prefixes in a resource group, or all when
// resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzurePublicIPPrefixes(
	_ context.Context, resourceGroup string,
) []driver.AzurePublicIPPrefix {
	out := make([]driver.AzurePublicIPPrefix, 0)

	values := m.azurePrefixes.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, clonePrefix(values[i]))
	}

	return out
}

// clonePrefix deep-copies the tag map and zones slice so stored and returned
// values never alias a caller's containers.
//
//nolint:gocritic // hugeParam: prefix mirrors the AzurePublicIPPrefixes driver signature.
func clonePrefix(prefix driver.AzurePublicIPPrefix) driver.AzurePublicIPPrefix {
	out := driver.AzurePublicIPPrefix{
		Name:          prefix.Name,
		ResourceGroup: prefix.ResourceGroup,
		Location:      prefix.Location,
		PrefixLength:  prefix.PrefixLength,
		IPPrefix:      prefix.IPPrefix,
		SKUName:       prefix.SKUName,
		SKUTier:       prefix.SKUTier,
	}

	if len(prefix.Tags) > 0 {
		out.Tags = copyTags(prefix.Tags)
	}

	if len(prefix.Zones) > 0 {
		out.Zones = append([]string(nil), prefix.Zones...)
	}

	return out
}

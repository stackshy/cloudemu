// Package bastion provides an in-memory implementation of the Azure Bastion
// (Microsoft.Network/bastionHosts) control plane. The ARM body is stored
// natively (its shape has no cross-cloud equivalent), keyed by (resourceGroup,
// name).
package bastion

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bastion/driver"
)

// Compile-time check that Mock implements the Bastion host store.
var _ driver.BastionHosts = (*Mock)(nil)

const (
	// skuStandard is the sku Azure applies when a request omits it.
	skuStandard = "Standard"
	// defaultScaleUnits is the scale-unit count Azure applies when a request
	// omits scaleUnits (the Standard-sku minimum).
	defaultScaleUnits = 2
	// dnsNamePrefix / dnsNameSuffix bracket the computed, globally-addressable
	// bastion DNS name (bst-<guid>.bastion.azure.com).
	dnsNamePrefix = "bst-"
	dnsNameSuffix = ".bastion.azure.com"
)

// Mock is an in-memory Azure Bastion host store.
type Mock struct {
	hosts *memstore.Store[driver.BastionHost]
	opts  *config.Options
}

// New creates a new Azure Bastion mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		hosts: memstore.New[driver.BastionHost](),
		opts:  opts,
	}
}

// key keys a store by (resourceGroup, name). ARM names are case-insensitive, so
// the key is lower-cased to resolve differently-cased GET/DELETE requests to the
// same resource and keep a re-PUT an in-place update rather than a duplicate.
// Only the map key is normalized; the stored body preserves the original casing.
func key(rg, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(name)
}

// bastionDNSName derives the stable, deterministic dnsName for a host from its
// (resourceGroup, name) key. The same host always yields the same name so it
// never drifts across GETs.
func bastionDNSName(seed string) string {
	return dnsNamePrefix + idgen.SyntheticGUID(seed) + dnsNameSuffix
}

// CreateOrUpdateBastionHost stores host as a full replace and reports whether it
// did not previously exist. It injects the sku/scaleUnits defaults and, on first
// create, a stable dnsName that is preserved unchanged across later updates.
//
//nolint:gocritic // hugeParam: value copied defensively.
func (m *Mock) CreateOrUpdateBastionHost(
	_ context.Context, rg, name string, host driver.BastionHost,
) (*driver.BastionHost, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "bastion host name is required")
	}

	existing, existed := m.hosts.Get(key(rg, name))

	stored := cloneHost(host)
	stored.Name = name
	stored.ResourceGroup = rg

	switch {
	case existed && existing.DNSName != "":
		stored.DNSName = existing.DNSName
	case stored.DNSName == "":
		stored.DNSName = bastionDNSName(key(rg, name))
	}

	if stored.SKUName == "" {
		stored.SKUName = skuStandard
	}

	if stored.ScaleUnits == 0 {
		stored.ScaleUnits = defaultScaleUnits
	}

	m.hosts.Set(key(rg, name), stored)

	out := cloneHost(stored)

	return &out, !existed, nil
}

// GetBastionHost returns the stored host.
func (m *Mock) GetBastionHost(_ context.Context, rg, name string) (*driver.BastionHost, error) {
	host, ok := m.hosts.Get(key(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bastion host %q not found", name)
	}

	out := cloneHost(host)

	return &out, nil
}

// DeleteBastionHost removes the stored host.
func (m *Mock) DeleteBastionHost(_ context.Context, rg, name string) error {
	if !m.hosts.Delete(key(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "bastion host %q not found", name)
	}

	return nil
}

// ListBastionHosts returns the hosts in rg, or all when rg is empty
// (subscription-wide list).
func (m *Mock) ListBastionHosts(_ context.Context, rg string) ([]driver.BastionHost, error) {
	all := m.hosts.SortedValues()

	out := make([]driver.BastionHost, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, cloneHost(all[i]))
	}

	return out, nil
}

// cloneHost deep-copies a host so stored and returned values never alias a
// caller's maps, slices or flag pointers.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneHost(h driver.BastionHost) driver.BastionHost {
	out := h
	out.Zones = append([]string(nil), h.Zones...)
	out.Tags = cloneStringMap(h.Tags)
	out.OtherProps = cloneAnyMap(h.OtherProps)
	out.IPConfigurations = append([]driver.BastionHostIPConfig(nil), h.IPConfigurations...)
	out.DisableCopyPaste = cloneBool(h.DisableCopyPaste)
	out.EnableTunneling = cloneBool(h.EnableTunneling)
	out.EnableIPConnect = cloneBool(h.EnableIPConnect)
	out.EnableShareableLink = cloneBool(h.EnableShareableLink)
	out.EnableFileCopy = cloneBool(h.EnableFileCopy)
	out.EnableKerberos = cloneBool(h.EnableKerberos)

	return out
}

// cloneBool copies a *bool so stored and returned flags never alias.
func cloneBool(p *bool) *bool {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

// cloneAnyMap deep-copies a generic JSON map so no request-owned map or slice is
// aliased into the store.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAnyValue(v)
	}

	return out
}

// cloneAnyValue recursively deep-copies a decoded JSON value.
func cloneAnyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneAnyValue(t[i])
		}

		return out
	default:
		return v
	}
}

// cloneStringMap copies a string map, or returns nil for an empty one.
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

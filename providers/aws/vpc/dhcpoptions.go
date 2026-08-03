package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateDHCPOptions creates a DHCP option set.
func (m *Mock) CreateDHCPOptions(_ context.Context, cfg driver.DHCPOptionsConfig) (*driver.DHCPOptions, error) {
	opt := &driver.DHCPOptions{
		ID:            idgen.GenerateID("dopt-"),
		Configuration: cloneDHCPConfig(cfg.Configuration),
		Tags:          copyTags(cfg.Tags),
	}
	m.dhcpOptions.Set(opt.ID, opt)

	out := cloneDHCPOptions(opt)

	return &out, nil
}

// DeleteDHCPOptions deletes a DHCP option set.
func (m *Mock) DeleteDHCPOptions(_ context.Context, id string) error {
	if !m.dhcpOptions.Delete(id) {
		return errors.Newf(errors.NotFound, "dhcp options %q not found", id)
	}

	return nil
}

// DescribeDHCPOptions returns DHCP option sets matching ids.
func (m *Mock) DescribeDHCPOptions(_ context.Context, ids []string) ([]driver.DHCPOptions, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.dhcpOptions, ids, cloneDHCPOptions), nil
}

// AssociateDHCPOptions associates a DHCP option set with a VPC. The special id
// "default" resets the VPC to the default (Amazon-provided) options.
func (m *Mock) AssociateDHCPOptions(_ context.Context, dhcpOptionsID, vpcID string) error {
	if !m.vpcs.Has(vpcID) {
		return errors.Newf(errors.InvalidArgument, "vpc %q not found", vpcID)
	}

	if dhcpOptionsID != "default" && !m.dhcpOptions.Has(dhcpOptionsID) {
		return errors.Newf(errors.NotFound, "dhcp options %q not found", dhcpOptionsID)
	}

	return nil
}

func cloneDHCPConfig(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}

	return out
}

func cloneDHCPOptions(d *driver.DHCPOptions) driver.DHCPOptions {
	out := *d
	out.Configuration = cloneDHCPConfig(d.Configuration)
	out.Tags = copyTags(d.Tags)

	return out
}

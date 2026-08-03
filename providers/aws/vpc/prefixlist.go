package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateManagedPrefixList creates a customer-managed prefix list.
func (m *Mock) CreateManagedPrefixList(_ context.Context, cfg driver.PrefixListConfig) (*driver.PrefixList, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "prefix list name is required")
	}

	if cfg.MaxEntries <= 0 {
		return nil, errors.New(errors.InvalidArgument, "maxEntries must be greater than zero")
	}

	pl := &driver.PrefixList{
		ID:            idgen.GenerateID("pl-"),
		Name:          cfg.Name,
		AddressFamily: orDefaultStr(cfg.AddressFamily, "IPv4"),
		MaxEntries:    cfg.MaxEntries,
		State:         "create-complete",
		Version:       1,
		Entries:       cloneEntries(cfg.Entries),
		Tags:          copyTags(cfg.Tags),
	}
	m.prefixLists.Set(pl.ID, pl)

	out := clonePrefixList(pl)

	return &out, nil
}

// DeleteManagedPrefixList deletes a managed prefix list.
func (m *Mock) DeleteManagedPrefixList(_ context.Context, id string) (*driver.PrefixList, error) {
	pl, ok := m.prefixLists.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prefix list %q not found", id)
	}

	pl.State = "delete-complete"

	m.prefixLists.Delete(id)

	out := clonePrefixList(pl)

	return &out, nil
}

// DescribeManagedPrefixLists returns prefix lists matching ids.
func (m *Mock) DescribeManagedPrefixLists(_ context.Context, ids []string) ([]driver.PrefixList, error) {
	return describeResources(m.prefixLists, ids, clonePrefixList), nil
}

// GetManagedPrefixListEntries returns the entries of a prefix list.
func (m *Mock) GetManagedPrefixListEntries(_ context.Context, id string) ([]driver.PrefixListEntry, error) {
	pl, ok := m.prefixLists.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prefix list %q not found", id)
	}

	return cloneEntries(pl.Entries), nil
}

func cloneEntries(in []driver.PrefixListEntry) []driver.PrefixListEntry {
	if len(in) == 0 {
		return nil
	}

	return append([]driver.PrefixListEntry(nil), in...)
}

func clonePrefixList(p *driver.PrefixList) driver.PrefixList {
	out := *p
	out.Entries = cloneEntries(p.Entries)
	out.Tags = copyTags(p.Tags)

	return out
}

// ModifyManagedPrefixList adds and/or removes entries, bumping the version.
//
//nolint:gocritic // slices match the driver signature.
func (m *Mock) ModifyManagedPrefixList(
	_ context.Context, id string, addEntries []driver.PrefixListEntry, removeCIDRs []string,
) (*driver.PrefixList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pl, ok := m.prefixLists.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prefix list %q not found", id)
	}

	remove := make(map[string]bool, len(removeCIDRs))
	for _, c := range removeCIDRs {
		remove[c] = true
	}

	kept := pl.Entries[:0:0]

	for _, e := range pl.Entries {
		if !remove[e.CIDR] {
			kept = append(kept, e)
		}
	}

	pl.Entries = append(kept, addEntries...)
	pl.Version++
	m.prefixLists.Set(id, pl)

	out := clonePrefixList(pl)

	return &out, nil
}

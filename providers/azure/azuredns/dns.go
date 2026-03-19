// Package azuredns provides an in-memory mock implementation of Azure DNS.
package azuredns

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/dns/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
)

// Compile-time check that Mock implements driver.DNS.
var _ driver.DNS = (*Mock)(nil)

// Mock is an in-memory mock implementation of the Azure DNS service.
type Mock struct {
	zones   *memstore.Store[driver.ZoneInfo]
	records *memstore.Store[driver.RecordInfo]
	opts    *config.Options
}

// New creates a new Azure DNS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		zones:   memstore.New[driver.ZoneInfo](),
		records: memstore.New[driver.RecordInfo](),
		opts:    opts,
	}
}

// recordKey builds the key used to store a record in the memstore.
// For weighted records (non-empty SetID), the SetID is appended.
func recordKey(zoneID, name, recordType, setID string) string {
	key := zoneID + ":" + name + ":" + recordType
	if setID != "" {
		key += ":" + setID
	}
	return key
}

// CreateZone creates a new Azure DNS zone.
func (m *Mock) CreateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "zone name is required")
	}

	id := idgen.AzureID(m.opts.AccountID, "cloud-mock", "Microsoft.Network", "dnsZones", cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	zone := driver.ZoneInfo{
		ID:          id,
		Name:        cfg.Name,
		Private:     cfg.Private,
		RecordCount: 0,
		Tags:        tags,
	}

	m.zones.Set(id, zone)

	result := zone
	return &result, nil
}

// DeleteZone deletes an Azure DNS zone by ID.
func (m *Mock) DeleteZone(_ context.Context, id string) error {
	if !m.zones.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "zone %q not found", id)
	}

	// Delete all records belonging to this zone.
	all := m.records.All()
	for key, rec := range all {
		if rec.ZoneID == id {
			m.records.Delete(key)
		}
	}

	return nil
}

// GetZone retrieves an Azure DNS zone by ID.
func (m *Mock) GetZone(_ context.Context, id string) (*driver.ZoneInfo, error) {
	zone, ok := m.zones.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", id)
	}

	result := zone
	return &result, nil
}

// ListZones returns all Azure DNS zones.
func (m *Mock) ListZones(_ context.Context) ([]driver.ZoneInfo, error) {
	all := m.zones.All()

	zones := make([]driver.ZoneInfo, 0, len(all))
	for _, z := range all {
		zones = append(zones, z)
	}

	return zones, nil
}

// CreateRecord creates a new DNS record set in the specified zone.
func (m *Mock) CreateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", cfg.ZoneID)
	}

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "record name is required")
	}

	if cfg.Type == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "record type is required")
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if m.records.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "record %q already exists in zone %q", cfg.Name, cfg.ZoneID)
	}

	values := make([]string, len(cfg.Values))
	copy(values, cfg.Values)

	var weight *int
	if cfg.Weight != nil {
		w := *cfg.Weight
		weight = &w
	}

	rec := driver.RecordInfo{
		ZoneID: cfg.ZoneID,
		Name:   cfg.Name,
		Type:   cfg.Type,
		TTL:    cfg.TTL,
		Values: values,
		Weight: weight,
		SetID:  cfg.SetID,
	}

	m.records.Set(key, rec)

	// Update zone record count.
	m.zones.Update(cfg.ZoneID, func(z driver.ZoneInfo) driver.ZoneInfo {
		z.RecordCount++
		return z
	})

	result := rec
	return &result, nil
}

// DeleteRecord deletes a DNS record set from the specified zone.
func (m *Mock) DeleteRecord(_ context.Context, zoneID, name, recordType string) error {
	if _, ok := m.zones.Get(zoneID); !ok {
		return cerrors.Newf(cerrors.NotFound, "zone %q not found", zoneID)
	}

	key := recordKey(zoneID, name, recordType, "")

	// Try without set ID first. If not found, search for any matching record with a set ID.
	if m.records.Delete(key) {
		m.zones.Update(zoneID, func(z driver.ZoneInfo) driver.ZoneInfo {
			z.RecordCount--
			return z
		})
		return nil
	}

	// Search for weighted records with a set ID.
	prefix := zoneID + ":" + name + ":" + recordType + ":"
	all := m.records.All()
	deleted := 0
	for k := range all {
		if strings.HasPrefix(k, prefix) {
			m.records.Delete(k)
			deleted++
		}
	}

	if deleted == 0 {
		return cerrors.Newf(cerrors.NotFound, "record %q of type %q not found in zone %q", name, recordType, zoneID)
	}

	m.zones.Update(zoneID, func(z driver.ZoneInfo) driver.ZoneInfo {
		z.RecordCount -= deleted
		return z
	})

	return nil
}

// GetRecord retrieves a DNS record set from the specified zone.
func (m *Mock) GetRecord(_ context.Context, zoneID, name, recordType string) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", zoneID)
	}

	key := recordKey(zoneID, name, recordType, "")

	rec, ok := m.records.Get(key)
	if ok {
		result := rec
		return &result, nil
	}

	// Search for weighted records with a set ID.
	prefix := zoneID + ":" + name + ":" + recordType + ":"
	all := m.records.All()
	for k, r := range all {
		if strings.HasPrefix(k, prefix) {
			result := r
			return &result, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "record %q of type %q not found in zone %q", name, recordType, zoneID)
}

// ListRecords returns all DNS record sets for the specified zone.
func (m *Mock) ListRecords(_ context.Context, zoneID string) ([]driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", zoneID)
	}

	filtered := m.records.Filter(func(_ string, rec driver.RecordInfo) bool {
		return rec.ZoneID == zoneID
	})

	records := make([]driver.RecordInfo, 0, len(filtered))
	for _, rec := range filtered {
		records = append(records, rec)
	}

	return records, nil
}

// UpdateRecord updates an existing DNS record set in the specified zone.
func (m *Mock) UpdateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", cfg.ZoneID)
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if _, ok := m.records.Get(key); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "record %q of type %q not found in zone %q", cfg.Name, cfg.Type, cfg.ZoneID)
	}

	values := make([]string, len(cfg.Values))
	copy(values, cfg.Values)

	var weight *int
	if cfg.Weight != nil {
		w := *cfg.Weight
		weight = &w
	}

	rec := driver.RecordInfo{
		ZoneID: cfg.ZoneID,
		Name:   cfg.Name,
		Type:   cfg.Type,
		TTL:    cfg.TTL,
		Values: values,
		Weight: weight,
		SetID:  cfg.SetID,
	}

	m.records.Set(key, rec)

	result := rec
	return &result, nil
}

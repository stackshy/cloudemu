// Package clouddns provides an in-memory mock implementation of GCP Cloud DNS.
package clouddns

import (
	"context"
	"maps"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements driver.DNS.
var _ driver.DNS = (*Mock)(nil)

// Mock is an in-memory mock implementation of the GCP Cloud DNS service.
type Mock struct {
	zones        *memstore.Store[driver.ZoneInfo]
	records      *memstore.Store[driver.RecordInfo]
	healthChecks *memstore.Store[driver.HealthCheckInfo]
	opts         *config.Options
}

// New creates a new Cloud DNS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		zones:        memstore.New[driver.ZoneInfo](),
		records:      memstore.New[driver.RecordInfo](),
		healthChecks: memstore.New[driver.HealthCheckInfo](),
		opts:         opts,
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

// CreateZone creates a new Cloud DNS managed zone.
func (m *Mock) CreateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "zone name is required")
	}

	// Managed-zone names are unique within a project. Reject a duplicate in the
	// same project (the wire always carries one); the portable API, which
	// creates with a zero scope, is unaffected.
	if cfg.Scope.Project != "" {
		for _, z := range m.zones.SortedValues() {
			if z.Name == cfg.Name && z.Scope.Project == cfg.Scope.Project {
				return nil, cerrors.Newf(cerrors.AlreadyExists,
					"managed zone %q already exists in project %q", cfg.Name, cfg.Scope.Project)
			}
		}
	}

	id := idgen.GenerateID("zone-")

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
		Scope:       cfg.Scope,
	}

	m.zones.Set(id, zone)

	result := zone

	return &result, nil
}

// DeleteZone deletes a Cloud DNS managed zone by ID.
func (m *Mock) DeleteZone(_ context.Context, id string) error {
	if !m.zones.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "managed zone %q not found", id)
	}

	// Delete all resource record sets belonging to this zone.
	all := m.records.All()
	for key, rec := range all {
		if rec.ZoneID == id {
			m.records.Delete(key)
		}
	}

	return nil
}

// GetZone retrieves a Cloud DNS managed zone by ID.
func (m *Mock) GetZone(_ context.Context, id string) (*driver.ZoneInfo, error) {
	zone, ok := m.zones.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", id)
	}

	result := zone

	return &result, nil
}

// ListZones returns the Cloud DNS managed zones visible under filter.
func (m *Mock) ListZones(_ context.Context, filter scope.Scope) ([]driver.ZoneInfo, error) {
	all := m.zones.SortedValues()

	zones := make([]driver.ZoneInfo, 0, len(all))
	for _, z := range all {
		if !z.Scope.Matches(filter) {
			continue
		}
		zones = append(zones, z)
	}

	return zones, nil
}

// UpdateZone applies the mutable fields (tags, scope) of an existing zone,
// mirroring CreateOrUpdate-on-existing. It matches the zone by name.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	for _, z := range m.zones.SortedValues() {
		// Match on name AND scope: the same zone name can exist in different
		// projects, and an update must not reach across into another.
		if z.Name != cfg.Name || !z.Scope.Matches(cfg.Scope) {
			continue
		}

		if cfg.Tags != nil {
			z.Tags = maps.Clone(cfg.Tags)
		}
		if !cfg.Scope.IsZero() {
			z.Scope = cfg.Scope
		}

		m.zones.Set(z.ID, z)

		result := z

		return &result, nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", cfg.Name)
}

// CreateRecord creates a new resource record set in the specified managed zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", cfg.ZoneID)
	}

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "record name is required")
	}

	if cfg.Type == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "record type is required")
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if m.records.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "resource record set %q already exists in zone %q", cfg.Name, cfg.ZoneID)
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

// DeleteRecord deletes a resource record set from the specified managed zone.
func (m *Mock) DeleteRecord(_ context.Context, zoneID, name, recordType string) error {
	if _, ok := m.zones.Get(zoneID); !ok {
		return cerrors.Newf(cerrors.NotFound, "managed zone %q not found", zoneID)
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
		return cerrors.Newf(cerrors.NotFound, "resource record set %q of type %q not found in zone %q", name, recordType, zoneID)
	}

	m.zones.Update(zoneID, func(z driver.ZoneInfo) driver.ZoneInfo {
		z.RecordCount -= deleted
		return z
	})

	return nil
}

// GetRecord retrieves a resource record set from the specified managed zone.
func (m *Mock) GetRecord(_ context.Context, zoneID, name, recordType string) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", zoneID)
	}

	key := recordKey(zoneID, name, recordType, "")

	rec, ok := m.records.Get(key)
	if ok {
		result := rec
		return &result, nil
	}

	// Search for weighted records (same name+type with a set ID). Iterate in
	// sorted-key order and return the lowest set ID, so a name+type with
	// several weighted records resolves to the same record every call rather
	// than a map-order-random one (#259).
	for _, r := range m.records.SortedValues() {
		if r.ZoneID == zoneID && r.Name == name && r.Type == recordType && r.SetID != "" {
			result := r
			return &result, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "resource record set %q of type %q not found in zone %q", name, recordType, zoneID)
}

// ListRecords returns all resource record sets for the specified managed zone.
func (m *Mock) ListRecords(_ context.Context, zoneID string) ([]driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", zoneID)
	}

	// SortedValues gives a stable order keyed by zoneID:name:type[:setID];
	// filter to this zone in that order so ListRecords is deterministic
	// (map iteration order must never reach the wire — #259).
	all := m.records.SortedValues()

	records := make([]driver.RecordInfo, 0, len(all))
	for _, rec := range all {
		if rec.ZoneID == zoneID {
			records = append(records, rec)
		}
	}

	return records, nil
}

// UpdateRecord updates an existing resource record set in the specified managed zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed zone %q not found", cfg.ZoneID)
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if _, ok := m.records.Get(key); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "resource record set %q of type %q not found in zone %q", cfg.Name, cfg.Type, cfg.ZoneID)
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

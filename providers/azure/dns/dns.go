// Package dns provides an in-memory mock implementation of Azure DNS.
package dns

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

// Mock is an in-memory mock implementation of the Azure DNS service.
type Mock struct {
	zones        *memstore.Store[driver.ZoneInfo]
	records      *memstore.Store[driver.RecordInfo]
	healthChecks *memstore.Store[driver.HealthCheckInfo]
	opts         *config.Options
}

// New creates a new Azure DNS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		zones:        memstore.New[driver.ZoneInfo](),
		records:      memstore.New[driver.RecordInfo](),
		healthChecks: memstore.New[driver.HealthCheckInfo](),
		opts:         opts,
	}
}

// cnameType is the DNS CNAME record type. Azure enforces CNAME coexistence
// rules against it (a CNAME cannot share a name with any other record set).
const cnameType = "CNAME"

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

	// Derive the ARM id's subscription/resource group from the request scope,
	// falling back to the account default and "cloud-mock" when unscoped.
	sub := cfg.Scope.Subscription
	if sub == "" {
		sub = m.opts.AccountID
	}
	rg := cfg.Scope.ResourceGroup
	if rg == "" {
		rg = "cloud-mock"
	}
	id := idgen.AzureID(sub, rg, "Microsoft.Network", "dnsZones", cfg.Name)

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

// ListZones returns the Azure DNS zones visible under filter. Iterating the
// store's SortedValues keeps the order deterministic (#259).
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
// matching it by name — ARM CreateOrUpdate-on-existing semantics. Identity
// and record count are preserved.
func (m *Mock) UpdateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	for _, z := range m.zones.SortedValues() {
		// Match on name AND scope: the same zone name can exist in different
		// resource groups, and an update must not reach across into another.
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

	return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", cfg.Name)
}

// CreateRecord creates a new DNS record set in the specified zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
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

	if m.hasCNAMEConflict(cfg.ZoneID, cfg.Name, cfg.Type) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"a CNAME record set cannot coexist with another record set of a different type at name %q", cfg.Name)
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if m.records.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "record %q already exists in zone %q", cfg.Name, cfg.ZoneID)
	}

	values := make([]string, len(cfg.Values))
	copy(values, cfg.Values)

	weight := copyWeight(cfg.Weight)

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

// hasCNAMEConflict reports whether creating a record of recordType at name
// would violate Azure's CNAME coexistence rule: a CNAME cannot share a name
// with any other record set, and no other record set can share a name with a
// CNAME. Both orders are checked. A same-type match (e.g. a second CNAME) is
// not a coexistence conflict here — it is handled as AlreadyExists.
func (m *Mock) hasCNAMEConflict(zoneID, name, recordType string) bool {
	newIsCNAME := strings.EqualFold(recordType, cnameType)
	existing := m.records.SortedValues()

	for i := range existing {
		r := &existing[i]
		if r.ZoneID != zoneID || !strings.EqualFold(r.Name, name) {
			continue
		}

		if strings.EqualFold(r.Type, recordType) {
			continue
		}

		if newIsCNAME || strings.EqualFold(r.Type, cnameType) {
			return true
		}
	}

	return false
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

	return nil, cerrors.Newf(cerrors.NotFound, "record %q of type %q not found in zone %q", name, recordType, zoneID)
}

// ListRecords returns all DNS record sets for the specified zone.
func (m *Mock) ListRecords(_ context.Context, zoneID string) ([]driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "zone %q not found", zoneID)
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

// UpdateRecord updates an existing DNS record set in the specified zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
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

	weight := copyWeight(cfg.Weight)

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

// copyWeight returns a deep copy of a weight pointer.
func copyWeight(w *int) *int {
	if w == nil {
		return nil
	}

	v := *w

	return &v
}

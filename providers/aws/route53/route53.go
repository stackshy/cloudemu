// Package route53 provides an in-memory mock implementation of AWS Route 53.
package route53

import (
	"context"
	"crypto/rand"
	"maps"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements driver.DNS.
var _ driver.DNS = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS Route 53 DNS service.
type Mock struct {
	zones        *memstore.Store[driver.ZoneInfo]
	records      *memstore.Store[driver.RecordInfo]
	healthChecks *memstore.Store[driver.HealthCheckInfo]
	opts         *config.Options

	tagsMu   sync.Mutex
	tagsByID map[string]map[string]string // ResourceId -> tags
}

// New creates a new Route 53 mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		zones:        memstore.New[driver.ZoneInfo](),
		records:      memstore.New[driver.RecordInfo](),
		healthChecks: memstore.New[driver.HealthCheckInfo](),
		opts:         opts,
		tagsByID:     map[string]map[string]string{},
	}
}

// ChangeResourceTags applies tag additions and key removals to a Route 53
// resource (hosted zone or health check) identified by ID.
func (m *Mock) ChangeResourceTags(_ context.Context, resourceID string, add map[string]string, remove []string) error {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if m.tagsByID[resourceID] == nil {
		m.tagsByID[resourceID] = map[string]string{}
	}

	for k, v := range add {
		m.tagsByID[resourceID][k] = v
	}

	for _, k := range remove {
		delete(m.tagsByID[resourceID], k)
	}

	return nil
}

// ListResourceTags returns the tags on a Route 53 resource by ID.
func (m *Mock) ListResourceTags(_ context.Context, resourceID string) (map[string]string, error) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	out := make(map[string]string, len(m.tagsByID[resourceID]))
	for k, v := range m.tagsByID[resourceID] {
		out[k] = v
	}

	return out, nil
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

// hostedZoneIDLen is the number of random characters after the "Z" prefix in an
// opaque Route 53 hosted-zone id (e.g. Z1D633PJN98FT9).
const hostedZoneIDLen = 13

// hostedZoneIDAlphabet is the uppercase-alphanumeric set real Route 53
// hosted-zone ids draw from.
const hostedZoneIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// newHostedZoneID returns an opaque uppercase hosted-zone id in Route 53's real
// shape ("Z" + random uppercase alphanumerics), not a sequential "zone-N" that
// breaks ARN construction and id pattern matching.
func newHostedZoneID() string {
	buf := make([]byte, hostedZoneIDLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not something the mock can recover from
		// meaningfully; fall back to the monotonic generator so ids stay unique.
		return "Z" + strings.ToUpper(idgen.GenerateID(""))
	}

	for i := range buf {
		buf[i] = hostedZoneIDAlphabet[int(buf[i])%len(hostedZoneIDAlphabet)]
	}

	return "Z" + string(buf)
}

// CreateZone creates a new DNS hosted zone.
func (m *Mock) CreateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "zone name is required")
	}

	id := newHostedZoneID()

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

// DeleteZone deletes a DNS hosted zone by ID.
func (m *Mock) DeleteZone(_ context.Context, id string) error {
	if !m.zones.Delete(id) {
		return errors.Newf(errors.NotFound, "zone %q not found", id)
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

// GetZone retrieves a DNS hosted zone by ID.
func (m *Mock) GetZone(_ context.Context, id string) (*driver.ZoneInfo, error) {
	zone, ok := m.zones.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", id)
	}

	result := zone

	return &result, nil
}

// ListZones returns all DNS hosted zones matching the scope filter. Route 53
// hosted zones are account-global, so zones are created unscoped and a zero
// filter (the AWS default) returns everything.
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

// UpdateZone applies the mutable fields (tags, scope) of an existing hosted
// zone, matching the zone by name — ARM CreateOrUpdate-on-existing semantics.
func (m *Mock) UpdateZone(_ context.Context, cfg driver.ZoneConfig) (*driver.ZoneInfo, error) {
	var (
		id    string
		found bool
	)

	for zid, z := range m.zones.All() {
		if z.Name == cfg.Name {
			id = zid
			found = true

			break
		}
	}

	if !found {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", cfg.Name)
	}

	m.zones.Update(id, func(z driver.ZoneInfo) driver.ZoneInfo {
		if cfg.Tags != nil {
			z.Tags = maps.Clone(cfg.Tags)
		}
		if !cfg.Scope.IsZero() {
			z.Scope = cfg.Scope
		}

		return z
	})

	updated, _ := m.zones.Get(id)
	result := updated

	return &result, nil
}

// CreateRecord creates a new DNS record in the specified zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", cfg.ZoneID)
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "record name is required")
	}

	if cfg.Type == "" {
		return nil, errors.New(errors.InvalidArgument, "record type is required")
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if m.records.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "record %q already exists in zone %q", cfg.Name, cfg.ZoneID)
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

// DeleteRecord deletes a DNS record from the specified zone.
func (m *Mock) DeleteRecord(_ context.Context, zoneID, name, recordType string) error {
	if _, ok := m.zones.Get(zoneID); !ok {
		return errors.Newf(errors.NotFound, "zone %q not found", zoneID)
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
		return errors.Newf(errors.NotFound, "record %q of type %q not found in zone %q", name, recordType, zoneID)
	}

	m.zones.Update(zoneID, func(z driver.ZoneInfo) driver.ZoneInfo {
		z.RecordCount -= deleted
		return z
	})

	return nil
}

// GetRecord retrieves a DNS record from the specified zone.
func (m *Mock) GetRecord(_ context.Context, zoneID, name, recordType string) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", zoneID)
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

	return nil, errors.Newf(errors.NotFound, "record %q of type %q not found in zone %q", name, recordType, zoneID)
}

// ListRecords returns all DNS records for the specified zone.
func (m *Mock) ListRecords(_ context.Context, zoneID string) ([]driver.RecordInfo, error) {
	if _, ok := m.zones.Get(zoneID); !ok {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", zoneID)
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

// UpdateRecord updates an existing DNS record in the specified zone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateRecord(_ context.Context, cfg driver.RecordConfig) (*driver.RecordInfo, error) {
	if _, ok := m.zones.Get(cfg.ZoneID); !ok {
		return nil, errors.Newf(errors.NotFound, "zone %q not found", cfg.ZoneID)
	}

	key := recordKey(cfg.ZoneID, cfg.Name, cfg.Type, cfg.SetID)

	if _, ok := m.records.Get(key); !ok {
		return nil, errors.Newf(errors.NotFound, "record %q of type %q not found in zone %q", cfg.Name, cfg.Type, cfg.ZoneID)
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

// copyWeight creates a copy of a weight pointer.
func copyWeight(w *int) *int {
	if w == nil {
		return nil
	}

	v := *w

	return &v
}

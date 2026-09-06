// Package privatedns provides an in-memory implementation of the Azure Private
// DNS (Microsoft.Network/privateDnsZones) store, plus its virtualNetworkLinks and
// record-set children. All ARM bodies are stored natively (their shape has no
// cross-cloud equivalent), keyed to match ARM addressing.
package privatedns

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// Compile-time check that Mock implements the Private DNS store.
var _ driver.PrivateDNS = (*Mock)(nil)

// soaSeedTTL is the TTL of the SOA record set seeded when a zone is created.
const soaSeedTTL int64 = 3600

// SOA seed field values, matching the shape real Azure Private DNS creates the
// apex SOA with. They round-trip verbatim through RecordData.
const (
	soaSeedHost        = "azureprivatedns.net"
	soaSeedEmail       = "azureprivatedns-hostmaster.microsoft.com"
	soaSeedSerial      = 1
	soaSeedRefreshTime = 3600
	soaSeedRetryTime   = 300
	soaSeedExpireTime  = 2419200
	soaSeedMinimumTTL  = 10
)

// Mock is an in-memory Private DNS store: zones plus their virtualNetworkLinks
// and record sets, each in its own keyed store.
type Mock struct {
	zones   *memstore.Store[driver.PrivateZone]
	links   *memstore.Store[driver.VirtualNetworkLink]
	records *memstore.Store[driver.RecordSet]
	opts    *config.Options
}

// New creates a new Private DNS mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		zones:   memstore.New[driver.PrivateZone](),
		links:   memstore.New[driver.VirtualNetworkLink](),
		records: memstore.New[driver.RecordSet](),
		opts:    opts,
	}
}

// zoneKey keys the zone store by (resourceGroup, zone). ARM names are
// case-insensitive, so the key is lower-cased; the stored body preserves the
// original casing.
func zoneKey(rg, zone string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(zone)
}

// linkKey keys the link store by (resourceGroup, zone, link).
func linkKey(rg, zone, name string) string {
	return zoneKey(rg, zone) + "/" + strings.ToLower(name)
}

// recordKey keys the record store by (resourceGroup, zone, recordType, name).
func recordKey(rg, zone, recordType, name string) string {
	return zoneKey(rg, zone) + "/" + strings.ToUpper(recordType) + "/" + strings.ToLower(name)
}

// --- zones ---

// CreateOrUpdatePrivateZone stores zone as a full replace and reports whether it
// did not previously exist. On first create it seeds the apex SOA record set.
//
//nolint:gocritic // hugeParam: value copied defensively.
func (m *Mock) CreateOrUpdatePrivateZone(
	_ context.Context, rg, name string, zone driver.PrivateZone,
) (*driver.PrivateZone, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "private dns zone name is required")
	}

	_, existed := m.zones.Get(zoneKey(rg, name))

	stored := cloneZone(zone)
	stored.Name = name
	stored.ResourceGroup = rg

	m.zones.Set(zoneKey(rg, name), stored)

	if !existed {
		m.seedSOA(rg, name)
	}

	out := m.withCounts(stored)

	return &out, !existed, nil
}

// GetPrivateZone returns the stored zone with its computed counts.
func (m *Mock) GetPrivateZone(_ context.Context, rg, name string) (*driver.PrivateZone, error) {
	zone, ok := m.zones.Get(zoneKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "private dns zone %q not found", name)
	}

	out := m.withCounts(zone)

	return &out, nil
}

// DeletePrivateZone removes the zone and cascades to its links and records.
func (m *Mock) DeletePrivateZone(_ context.Context, rg, name string) error {
	if !m.zones.Delete(zoneKey(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "private dns zone %q not found", name)
	}

	for _, l := range m.links.SortedValues() {
		if sameZone(l.ResourceGroup, l.ZoneName, rg, name) {
			m.links.Delete(linkKey(l.ResourceGroup, l.ZoneName, l.Name))
		}
	}

	for _, rs := range m.records.SortedValues() {
		if sameZone(rs.ResourceGroup, rs.ZoneName, rg, name) {
			m.records.Delete(recordKey(rs.ResourceGroup, rs.ZoneName, rs.RecordType, rs.Name))
		}
	}

	return nil
}

// ListPrivateZones returns the zones in rg, or all when rg is empty.
func (m *Mock) ListPrivateZones(_ context.Context, rg string) ([]driver.PrivateZone, error) {
	all := m.zones.SortedValues()

	out := make([]driver.PrivateZone, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, m.withCounts(all[i]))
	}

	return out, nil
}

// seedSOA writes the apex SOA record set created alongside a new zone, so
// numberOfRecordSets starts at 1.
func (m *Mock) seedSOA(rg, zone string) {
	ttl := soaSeedTTL
	rs := driver.RecordSet{
		Name:          "@",
		ZoneName:      zone,
		ResourceGroup: rg,
		RecordType:    driver.RecordTypeSOA,
		TTL:           &ttl,
		RecordData: map[string]any{
			"soaRecord": map[string]any{
				"host":         soaSeedHost,
				"email":        soaSeedEmail,
				"serialNumber": float64(soaSeedSerial),
				"refreshTime":  float64(soaSeedRefreshTime),
				"retryTime":    float64(soaSeedRetryTime),
				"expireTime":   float64(soaSeedExpireTime),
				"minimumTtl":   float64(soaSeedMinimumTTL),
			},
		},
	}
	m.records.Set(recordKey(rg, zone, driver.RecordTypeSOA, "@"), cloneRecord(rs))
}

// withCounts returns a copy of zone with the numberOf* counts computed from the
// live child stores.
//
//nolint:gocritic // hugeParam: value copied defensively.
func (m *Mock) withCounts(zone driver.PrivateZone) driver.PrivateZone {
	out := cloneZone(zone)

	for _, l := range m.links.SortedValues() {
		if !sameZone(l.ResourceGroup, l.ZoneName, zone.ResourceGroup, zone.Name) {
			continue
		}

		out.NumberOfVirtualNetworkLinks++

		if l.RegistrationEnabled != nil && *l.RegistrationEnabled {
			out.NumberOfVirtualNetworkLinksWithRegistration++
		}
	}

	for _, rs := range m.records.SortedValues() {
		if sameZone(rs.ResourceGroup, rs.ZoneName, zone.ResourceGroup, zone.Name) {
			out.NumberOfRecordSets++
		}
	}

	return out
}

// --- virtual network links ---

// CreateOrUpdateVirtualNetworkLink stores link as a full replace under an
// existing zone.
//
//nolint:gocritic // hugeParam: value copied defensively.
func (m *Mock) CreateOrUpdateVirtualNetworkLink(
	_ context.Context, rg, zone, name string, link driver.VirtualNetworkLink,
) (*driver.VirtualNetworkLink, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "virtual network link name is required")
	}

	if !m.zones.Has(zoneKey(rg, zone)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "private dns zone %q not found", zone)
	}

	_, existed := m.links.Get(linkKey(rg, zone, name))

	stored := cloneLink(link)
	stored.Name = name
	stored.ZoneName = zone
	stored.ResourceGroup = rg

	m.links.Set(linkKey(rg, zone, name), stored)

	out := cloneLink(stored)

	return &out, !existed, nil
}

// GetVirtualNetworkLink returns the stored link.
func (m *Mock) GetVirtualNetworkLink(_ context.Context, rg, zone, name string) (*driver.VirtualNetworkLink, error) {
	link, ok := m.links.Get(linkKey(rg, zone, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "virtual network link %q not found", name)
	}

	out := cloneLink(link)

	return &out, nil
}

// DeleteVirtualNetworkLink removes the stored link.
func (m *Mock) DeleteVirtualNetworkLink(_ context.Context, rg, zone, name string) error {
	if !m.links.Delete(linkKey(rg, zone, name)) {
		return cerrors.Newf(cerrors.NotFound, "virtual network link %q not found", name)
	}

	return nil
}

// ListVirtualNetworkLinks returns the links under a zone.
func (m *Mock) ListVirtualNetworkLinks(_ context.Context, rg, zone string) ([]driver.VirtualNetworkLink, error) {
	all := m.links.SortedValues()

	out := make([]driver.VirtualNetworkLink, 0, len(all))

	for i := range all {
		if sameZone(all[i].ResourceGroup, all[i].ZoneName, rg, zone) {
			out = append(out, cloneLink(all[i]))
		}
	}

	return out, nil
}

// --- record sets ---

// CreateOrUpdateRecordSet stores rs as a full replace under an existing zone.
//
//nolint:gocritic // hugeParam: value copied defensively.
func (m *Mock) CreateOrUpdateRecordSet(
	_ context.Context, rg, zone, recordType, name string, rs driver.RecordSet,
) (*driver.RecordSet, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "record set name is required")
	}

	if !m.zones.Has(zoneKey(rg, zone)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "private dns zone %q not found", zone)
	}

	_, existed := m.records.Get(recordKey(rg, zone, recordType, name))

	stored := cloneRecord(rs)
	stored.Name = name
	stored.ZoneName = zone
	stored.ResourceGroup = rg
	stored.RecordType = strings.ToUpper(recordType)

	m.records.Set(recordKey(rg, zone, recordType, name), stored)

	out := cloneRecord(stored)

	return &out, !existed, nil
}

// GetRecordSet returns the stored record set.
func (m *Mock) GetRecordSet(_ context.Context, rg, zone, recordType, name string) (*driver.RecordSet, error) {
	rs, ok := m.records.Get(recordKey(rg, zone, recordType, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "record set %q not found", name)
	}

	out := cloneRecord(rs)

	return &out, nil
}

// DeleteRecordSet removes the stored record set.
func (m *Mock) DeleteRecordSet(_ context.Context, rg, zone, recordType, name string) error {
	if !m.records.Delete(recordKey(rg, zone, recordType, name)) {
		return cerrors.Newf(cerrors.NotFound, "record set %q not found", name)
	}

	return nil
}

// ListRecordSets returns every record set under a zone.
func (m *Mock) ListRecordSets(_ context.Context, rg, zone string) ([]driver.RecordSet, error) {
	return m.filterRecords(rg, zone, ""), nil
}

// ListRecordSetsByType returns the zone's record sets of one type.
func (m *Mock) ListRecordSetsByType(_ context.Context, rg, zone, recordType string) ([]driver.RecordSet, error) {
	return m.filterRecords(rg, zone, recordType), nil
}

// filterRecords returns the zone's record sets, optionally filtered by type.
func (m *Mock) filterRecords(rg, zone, recordType string) []driver.RecordSet {
	all := m.records.SortedValues()

	out := make([]driver.RecordSet, 0, len(all))

	for i := range all {
		if !sameZone(all[i].ResourceGroup, all[i].ZoneName, rg, zone) {
			continue
		}

		if recordType != "" && !strings.EqualFold(all[i].RecordType, recordType) {
			continue
		}

		out = append(out, cloneRecord(all[i]))
	}

	return out
}

// sameZone reports whether (rg1, zone1) and (rg2, zone2) identify the same zone,
// case-insensitively.
func sameZone(rg1, zone1, rg2, zone2 string) bool {
	return strings.EqualFold(rg1, rg2) && strings.EqualFold(zone1, zone2)
}

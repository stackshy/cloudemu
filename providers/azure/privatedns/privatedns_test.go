package privatedns_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/privatedns"
	"github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

const (
	rg   = "rg-1"
	zone = "example.internal"
)

func newMock() *privatedns.Mock {
	return privatedns.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func boolPtr(b bool) *bool { return &b }

func int64Ptr(n int64) *int64 { return &n }

// TestZoneSeedsSOA proves creating a zone seeds the apex SOA record set, so the
// computed numberOfRecordSets starts at 1.
func TestZoneSeedsSOA(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	stored, created, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	if !created {
		t.Fatal("created = false, want true")
	}

	if stored.NumberOfRecordSets != 1 {
		t.Errorf("numberOfRecordSets = %d, want 1 (auto-SOA)", stored.NumberOfRecordSets)
	}

	soa, err := m.GetRecordSet(ctx, rg, zone, driver.RecordTypeSOA, "@")
	requireNoError(t, err, "get seeded SOA")

	if soa.RecordType != driver.RecordTypeSOA {
		t.Errorf("seeded record type = %q, want SOA", soa.RecordType)
	}
}

// TestZoneCounts proves numberOfVirtualNetworkLinks and
// numberOfVirtualNetworkLinksWithRegistration are computed from the live links.
func TestZoneCounts(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	_, _, err = m.CreateOrUpdateVirtualNetworkLink(ctx, rg, zone, "link-reg",
		driver.VirtualNetworkLink{RegistrationEnabled: boolPtr(true)})
	requireNoError(t, err, "create link-reg")

	_, _, err = m.CreateOrUpdateVirtualNetworkLink(ctx, rg, zone, "link-noreg",
		driver.VirtualNetworkLink{RegistrationEnabled: boolPtr(false)})
	requireNoError(t, err, "create link-noreg")

	got, err := m.GetPrivateZone(ctx, rg, zone)
	requireNoError(t, err, "get zone")

	if got.NumberOfVirtualNetworkLinks != 2 {
		t.Errorf("numberOfVirtualNetworkLinks = %d, want 2", got.NumberOfVirtualNetworkLinks)
	}

	if got.NumberOfVirtualNetworkLinksWithRegistration != 1 {
		t.Errorf("numberOfVirtualNetworkLinksWithRegistration = %d, want 1",
			got.NumberOfVirtualNetworkLinksWithRegistration)
	}
}

// TestLinkRegistrationFalseRoundTrips proves an explicit false registration flag
// is preserved distinctly (not swallowed to nil/true).
func TestLinkRegistrationFalseRoundTrips(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	_, _, err = m.CreateOrUpdateVirtualNetworkLink(ctx, rg, zone, "link-1",
		driver.VirtualNetworkLink{RegistrationEnabled: boolPtr(false), VirtualNetworkID: "/vnets/v1"})
	requireNoError(t, err, "create link")

	got, err := m.GetVirtualNetworkLink(ctx, rg, zone, "link-1")
	requireNoError(t, err, "get link")

	if got.RegistrationEnabled == nil {
		t.Fatal("registrationEnabled = nil, want explicit false")
	}

	if *got.RegistrationEnabled {
		t.Error("registrationEnabled = true, want false")
	}

	if got.VirtualNetworkID != "/vnets/v1" {
		t.Errorf("virtualNetworkID = %q, want /vnets/v1", got.VirtualNetworkID)
	}
}

// TestLinkRequiresZone proves a link write against a missing zone is NotFound.
func TestLinkRequiresZone(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateVirtualNetworkLink(context.Background(), rg, "absent", "link-1",
		driver.VirtualNetworkLink{})
	if !cerrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

// TestRecordRoundTrip proves a record set's ttl and generic record data survive.
func TestRecordRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	data := map[string]any{"aRecords": []any{map[string]any{"ipv4Address": "10.0.0.1"}}}
	_, created, err := m.CreateOrUpdateRecordSet(ctx, rg, zone, driver.RecordTypeA, "www",
		driver.RecordSet{TTL: int64Ptr(300), RecordData: data})
	requireNoError(t, err, "create record")

	if !created {
		t.Fatal("created = false, want true")
	}

	got, err := m.GetRecordSet(ctx, rg, zone, "a", "www") // case-insensitive type
	requireNoError(t, err, "get record")

	if got.TTL == nil || *got.TTL != 300 {
		t.Errorf("ttl = %v, want 300", got.TTL)
	}

	if got.RecordType != driver.RecordTypeA {
		t.Errorf("record type = %q, want A", got.RecordType)
	}
}

// TestDeleteZoneCascades proves deleting a zone removes its links and records.
func TestDeleteZoneCascades(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	_, _, err = m.CreateOrUpdateVirtualNetworkLink(ctx, rg, zone, "link-1", driver.VirtualNetworkLink{})
	requireNoError(t, err, "create link")

	requireNoError(t, m.DeletePrivateZone(ctx, rg, zone), "delete zone")

	if _, err = m.GetVirtualNetworkLink(ctx, rg, zone, "link-1"); !cerrors.IsNotFound(err) {
		t.Errorf("link after cascade = %v, want NotFound", err)
	}

	if _, err = m.GetRecordSet(ctx, rg, zone, driver.RecordTypeSOA, "@"); !cerrors.IsNotFound(err) {
		t.Errorf("SOA after cascade = %v, want NotFound", err)
	}
}

// TestListRecordSetsByType filters to one record type; ListRecordSets returns all.
func TestListRecordSetsByType(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	_, _, err = m.CreateOrUpdateRecordSet(ctx, rg, zone, driver.RecordTypeA, "a1", driver.RecordSet{})
	requireNoError(t, err, "create A")

	aRecs, err := m.ListRecordSetsByType(ctx, rg, zone, driver.RecordTypeA)
	requireNoError(t, err, "list by type A")

	if len(aRecs) != 1 {
		t.Errorf("A records = %d, want 1", len(aRecs))
	}

	all, err := m.ListRecordSets(ctx, rg, zone)
	requireNoError(t, err, "list all")

	if len(all) != 2 { // seeded SOA + the A record
		t.Errorf("all records = %d, want 2 (SOA + A)", len(all))
	}
}

package clouddns_test

import (
	"context"
	"errors"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

// seedZoneWithRecord creates a zone and a single A record set in it, returning
// the record set as stored so tests can build matching/mismatching deletions.
func seedZoneWithRecord(t *testing.T, svc *dns.Service, ctx context.Context,
	zone, dnsName string, rec *dns.ResourceRecordSet) {
	t.Helper()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: zone, DnsName: dnsName,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create zone: %v", err)
	}

	if _, err := svc.Changes.Create(testProject, zone, &dns.Change{
		Additions: []*dns.ResourceRecordSet{rec},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("seed record: %v", err)
	}
}

// findRecord returns the stored A record set at name, or nil if absent.
func findRecord(t *testing.T, svc *dns.Service, ctx context.Context, zone, name string) *dns.ResourceRecordSet {
	t.Helper()

	resp, err := svc.ResourceRecordSets.List(testProject, zone).Name(name).Type("A").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List rrsets: %v", err)
	}

	if len(resp.Rrsets) == 0 {
		return nil
	}

	return resp.Rrsets[0]
}

// TestSDKCloudDNSDeleteWrongTTLRejected asserts a deletion whose TTL does not
// match the stored record set is refused 412 conditionNotMet and deletes
// nothing.
func TestSDKCloudDNSDeleteWrongTTLRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	name := "www.ttl.example.com."
	seedZoneWithRecord(t, svc, ctx, "ttl-zone", "ttl.example.com.",
		&dns.ResourceRecordSet{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}})

	_, err := svc.Changes.Create(testProject, "ttl-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{
			{Name: name, Type: "A", Ttl: 600, Rrdatas: []string{"192.0.2.1"}},
		},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 412 {
		t.Fatalf("wrong-TTL delete: got %v, want 412", err)
	}

	if rec := findRecord(t, svc, ctx, "ttl-zone", name); rec == nil || rec.Ttl != 300 {
		t.Fatalf("record must be untouched after rejected delete, got %+v", rec)
	}
}

// TestSDKCloudDNSDeleteWrongRrdataRejected asserts a deletion whose rrdatas do
// not match the stored record set is refused 412 and deletes nothing.
func TestSDKCloudDNSDeleteWrongRrdataRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	name := "www.rr.example.com."
	seedZoneWithRecord(t, svc, ctx, "rr-zone", "rr.example.com.",
		&dns.ResourceRecordSet{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1", "192.0.2.2"}})

	_, err := svc.Changes.Create(testProject, "rr-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{
			{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.9"}},
		},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 412 {
		t.Fatalf("wrong-rrdata delete: got %v, want 412", err)
	}

	if rec := findRecord(t, svc, ctx, "rr-zone", name); rec == nil || len(rec.Rrdatas) != 2 {
		t.Fatalf("record must be untouched after rejected delete, got %+v", rec)
	}
}

// TestSDKCloudDNSDeleteExactMatch asserts a deletion that matches the stored
// record set exactly (TTL + rrdatas, order-independent) succeeds and removes it.
func TestSDKCloudDNSDeleteExactMatch(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	name := "www.ok.example.com."
	seedZoneWithRecord(t, svc, ctx, "ok-zone", "ok.example.com.",
		&dns.ResourceRecordSet{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1", "192.0.2.2"}})

	// rrdatas listed in a different order still match — order-independent.
	if _, err := svc.Changes.Create(testProject, "ok-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{
			{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.2", "192.0.2.1"}},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("exact-match delete: %v", err)
	}

	if rec := findRecord(t, svc, ctx, "ok-zone", name); rec != nil {
		t.Fatalf("record should be deleted, still present: %+v", rec)
	}
}

// TestSDKCloudDNSMalformedAdditionRejectedBeforeApply asserts a batch with a
// malformed addition (no rrdatas) is rejected up front so its paired deletion
// never lands — the zone is left unchanged.
func TestSDKCloudDNSMalformedAdditionRejectedBeforeApply(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	name := "www.atomic.example.com."
	seedZoneWithRecord(t, svc, ctx, "atomic-zone", "atomic.example.com.",
		&dns.ResourceRecordSet{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}})

	// Delete the existing record and add a malformed replacement in one batch.
	_, err := svc.Changes.Create(testProject, "atomic-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{
			{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
		},
		Additions: []*dns.ResourceRecordSet{
			{Name: name, Type: "A", Ttl: 300},
		},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("malformed addition: got %v, want 400", err)
	}

	if rec := findRecord(t, svc, ctx, "atomic-zone", name); rec == nil || rec.Rrdatas[0] != "192.0.2.1" {
		t.Fatalf("deletion must not have applied, record changed: %+v", rec)
	}
}

// TestSDKCloudDNSCreateEmptyDNSNameRejected asserts a zone create with no
// dnsName is refused 400 rather than silently defaulting to the zone name.
func TestSDKCloudDNSCreateEmptyDNSNameRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	_, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{Name: "no-dnsname"}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("empty dnsName: got %v, want 400", err)
	}
}

// TestSDKCloudDNSCreateBadZoneNameRejected asserts a zone name that violates
// Cloud DNS's [a-z]([-a-z0-9]*[a-z0-9])? grammar is refused 400.
func TestSDKCloudDNSCreateBadZoneNameRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	for _, bad := range []string{"Bad-Zone", "1zone", "zone-", "with_underscore"} {
		_, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
			Name: bad, DnsName: "x.example.com.",
		}).Context(ctx).Do()

		var gerr *googleapi.Error
		if !errors.As(err, &gerr) || gerr.Code != 400 {
			t.Fatalf("bad zone name %q: got %v, want 400", bad, err)
		}
	}
}

// TestSDKCloudDNSCreateNonFQDNRejected asserts a dnsName without a trailing dot
// is refused 400.
func TestSDKCloudDNSCreateNonFQDNRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	_, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: "nofqdn", DnsName: "example.com",
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("non-FQDN dnsName: got %v, want 400", err)
	}
}

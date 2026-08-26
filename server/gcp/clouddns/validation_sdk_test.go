package clouddns_test

import (
	"context"
	"errors"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

// apiCode extracts the HTTP status code from a googleapi error, failing the test
// when err is not a googleapi.Error.
func apiCode(t *testing.T, err error) int {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("want googleapi.Error, got %v", err)
	}

	return gerr.Code
}

// createZone is a test helper that creates a managed zone and fails on error.
func createZone(t *testing.T, svc *dns.Service, z *dns.ManagedZone) *dns.ManagedZone {
	t.Helper()

	created, err := svc.ManagedZones.Create(testProject, z).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create(%s): %v", z.Name, err)
	}

	return created
}

// addRecord is a test helper that applies a single-addition change.
func addRecord(t *testing.T, svc *dns.Service, zone string, rr *dns.ResourceRecordSet) error {
	t.Helper()

	_, err := svc.Changes.Create(testProject, zone, &dns.Change{
		Additions: []*dns.ResourceRecordSet{rr},
	}).Context(context.Background()).Do()

	return err
}

// TestSDKDeleteNonEmptyZoneRejected (B1) asserts a zone holding a user record set
// cannot be deleted (400 containerNotEmpty), while a zone with only the apex
// SOA/NS deletes cleanly.
func TestSDKDeleteNonEmptyZoneRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "full-zone", DnsName: "full.example.com."})

	if err := addRecord(t, svc, "full-zone", &dns.ResourceRecordSet{
		Name: "www.full.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"},
	}); err != nil {
		t.Fatalf("addRecord: %v", err)
	}

	err := svc.ManagedZones.Delete(testProject, "full-zone").Context(ctx).Do()
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("Delete non-empty zone: got %d, want 400 containerNotEmpty", code)
	}

	// A zone with only the auto-created apex records deletes without error.
	createZone(t, svc, &dns.ManagedZone{Name: "empty-zone", DnsName: "empty.example.com."})
	if err := svc.ManagedZones.Delete(testProject, "empty-zone").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete apex-only zone: %v", err)
	}
}

// TestSDKCNAMECoexistenceRejected (B2) asserts a CNAME cannot coexist with
// another record type at the same name, in either add order.
func TestSDKCNAMECoexistenceRejected(t *testing.T) {
	svc := newDNSService(t)

	createZone(t, svc, &dns.ManagedZone{Name: "cname-zone", DnsName: "cname.example.com."})

	// A exists, then adding a CNAME at the same name conflicts.
	if err := addRecord(t, svc, "cname-zone", &dns.ResourceRecordSet{
		Name: "a.cname.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"},
	}); err != nil {
		t.Fatalf("add A: %v", err)
	}

	err := addRecord(t, svc, "cname-zone", &dns.ResourceRecordSet{
		Name: "a.cname.example.com.", Type: "CNAME", Ttl: 300, Rrdatas: []string{"target.example.com."},
	})
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("add CNAME beside A: got %d, want 400", code)
	}

	// CNAME exists, then adding an A at the same name conflicts.
	if err := addRecord(t, svc, "cname-zone", &dns.ResourceRecordSet{
		Name: "c.cname.example.com.", Type: "CNAME", Ttl: 300, Rrdatas: []string{"target.example.com."},
	}); err != nil {
		t.Fatalf("add CNAME: %v", err)
	}

	err = addRecord(t, svc, "cname-zone", &dns.ResourceRecordSet{
		Name: "c.cname.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.2"},
	})
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("add A beside CNAME: got %d, want 400", code)
	}
}

// apexRecord fetches the zone's apex record set of the given type via the SDK.
func apexRecord(t *testing.T, svc *dns.Service, zone, dnsName, rtype string) *dns.ResourceRecordSet {
	t.Helper()

	list, err := svc.ResourceRecordSets.List(testProject, zone).
		Name(dnsName).Type(rtype).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("List apex %s: %v", rtype, err)
	}

	if len(list.Rrsets) != 1 {
		t.Fatalf("apex %s rrsets = %d, want 1", rtype, len(list.Rrsets))
	}

	return list.Rrsets[0]
}

// TestSDKApexNSSOADeletionRejected (B3) asserts the apex NS and SOA record sets
// cannot be removed by a plain deletion change.
func TestSDKApexNSSOADeletionRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	const dnsName = "apex.example.com."
	createZone(t, svc, &dns.ManagedZone{Name: "apex-zone", DnsName: dnsName})

	for _, rtype := range []string{"NS", "SOA"} {
		rr := apexRecord(t, svc, "apex-zone", dnsName, rtype)

		_, err := svc.Changes.Create(testProject, "apex-zone", &dns.Change{
			Deletions: []*dns.ResourceRecordSet{rr},
		}).Context(ctx).Do()
		if code := apiCode(t, err); code != 400 {
			t.Fatalf("delete apex %s: got %d, want 400", rtype, code)
		}
	}
}

// TestSDKFreshZoneChangeLogged (B4) asserts a freshly created zone already has
// the initial apex-provisioning change (id "0") in its change log.
func TestSDKFreshZoneChangeLogged(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "seed-zone", DnsName: "seed.example.com."})

	list, err := svc.Changes.List(testProject, "seed-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.List: %v", err)
	}

	if len(list.Changes) != 1 {
		t.Fatalf("fresh-zone changes = %d, want 1: %+v", len(list.Changes), list.Changes)
	}

	c := list.Changes[0]
	if c.Id != "0" || c.Status != "done" {
		t.Fatalf("seed change = id %q status %q, want id \"0\" status done", c.Id, c.Status)
	}

	if len(userRrsets(c.Additions)) != 0 || len(c.Additions) != 2 {
		t.Fatalf("seed change additions = %+v, want the apex SOA+NS", c.Additions)
	}
}

// TestSDKChangeIdSequentialAfterPatch (B5) asserts change ids stay sequential
// within a zone and are not perturbed by a managed-zone patch (which uses a
// separate operation-id counter).
func TestSDKChangeIdSequentialAfterPatch(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "seq-zone", DnsName: "seq.example.com."})

	first, err := svc.Changes.Create(testProject, "seq-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{Name: "a.seq.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("first change: %v", err)
	}

	if first.Id != "1" {
		t.Fatalf("first user change id = %q, want \"1\" (after seeded \"0\")", first.Id)
	}

	// A zone patch must not consume a change id.
	if _, err := svc.ManagedZones.Patch(testProject, "seq-zone", &dns.ManagedZone{
		Description: "patched",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	second, err := svc.Changes.Create(testProject, "seq-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{Name: "b.seq.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.2"}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("second change: %v", err)
	}

	if second.Id != "2" {
		t.Fatalf("second user change id = %q, want \"2\" (sequential, patch did not skip)", second.Id)
	}
}

// TestSDKDnssecConfigRoundTrip (B6) asserts a zone's dnssecConfig survives
// create → get (and a subsequent patch that omits it).
func TestSDKDnssecConfigRoundTrip(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{
		Name:    "dnssec-zone",
		DnsName: "dnssec.example.com.",
		DnssecConfig: &dns.ManagedZoneDnsSecConfig{
			State:        "on",
			NonExistence: "nsec3",
			DefaultKeySpecs: []*dns.DnsKeySpec{
				{Algorithm: "rsasha256", KeyLength: 2048, KeyType: "keySigning"},
				{Algorithm: "rsasha256", KeyLength: 1024, KeyType: "zoneSigning"},
			},
		},
	})

	got, err := svc.ManagedZones.Get(testProject, "dnssec-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertDnssecOn(t, got.DnssecConfig)

	// A patch that does not touch dnssecConfig must preserve it.
	if _, err := svc.ManagedZones.Patch(testProject, "dnssec-zone", &dns.ManagedZone{
		Description: "unrelated",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	after, err := svc.ManagedZones.Get(testProject, "dnssec-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	assertDnssecOn(t, after.DnssecConfig)
}

// assertDnssecOn checks the round-tripped dnssecConfig matches what B6 created.
func assertDnssecOn(t *testing.T, c *dns.ManagedZoneDnsSecConfig) {
	t.Helper()

	if c == nil {
		t.Fatal("dnssecConfig dropped after round-trip")
	}

	if c.State != "on" || c.NonExistence != "nsec3" {
		t.Fatalf("dnssecConfig = state %q nonExistence %q, want on/nsec3", c.State, c.NonExistence)
	}

	if len(c.DefaultKeySpecs) != 2 {
		t.Fatalf("defaultKeySpecs = %d, want 2", len(c.DefaultKeySpecs))
	}

	if c.DefaultKeySpecs[0].KeyLength != 2048 || c.DefaultKeySpecs[0].KeyType != "keySigning" {
		t.Fatalf("keySpec[0] = %+v, want 2048/keySigning", c.DefaultKeySpecs[0])
	}
}

// TestSDKPrivateVisibilityNetworksRoundTrip (B7) asserts a private zone's
// privateVisibilityConfig.networks survive create → get.
func TestSDKPrivateVisibilityNetworksRoundTrip(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	const netURL = "https://www.googleapis.com/compute/v1/projects/demo/global/networks/vpc-a"

	createZone(t, svc, &dns.ManagedZone{
		Name:       "private-zone",
		DnsName:    "private.example.com.",
		Visibility: "private",
		PrivateVisibilityConfig: &dns.ManagedZonePrivateVisibilityConfig{
			Networks: []*dns.ManagedZonePrivateVisibilityConfigNetwork{{NetworkUrl: netURL}},
		},
	})

	got, err := svc.ManagedZones.Get(testProject, "private-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Visibility != "private" {
		t.Fatalf("visibility = %q, want private", got.Visibility)
	}

	if got.PrivateVisibilityConfig == nil || len(got.PrivateVisibilityConfig.Networks) != 1 {
		t.Fatalf("privateVisibilityConfig.networks dropped: %+v", got.PrivateVisibilityConfig)
	}

	if got.PrivateVisibilityConfig.Networks[0].NetworkUrl != netURL {
		t.Fatalf("networkUrl = %q, want %q", got.PrivateVisibilityConfig.Networks[0].NetworkUrl, netURL)
	}
}

package clouddns_test

import (
	"context"
	"strings"
	"testing"

	dns "google.golang.org/api/dns/v1"
)

// TestSDKAdditionOutsideZoneDNSNameRejected guards a real-user e2e finding: a
// record set whose name is unrelated to the zone's dnsName used to be accepted
// silently. Real Cloud DNS requires every record set's name to be the zone's
// own DNS name or a subdomain of it.
func TestSDKAdditionOutsideZoneDNSNameRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "outside-zone", DnsName: "outside.example.com."})

	err := addRecord(t, svc, "outside-zone", &dns.ResourceRecordSet{
		Name: "www.totally-different-domain.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"},
	})
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("out-of-zone addition: got %v, want 400", err)
	}

	if rec := findRecord(t, svc, ctx, "outside-zone", "www.totally-different-domain.com."); rec != nil {
		t.Fatalf("out-of-zone record must not have been created: %+v", rec)
	}
}

// TestSDKAdditionOutsideZoneDNSNameRejectsLabelBoundary asserts the within-zone
// check matches on label boundaries rather than a raw string suffix: a name
// that merely ends with the same letters as the zone's dnsName (but isn't
// actually a subdomain of it) must still be rejected.
func TestSDKAdditionOutsideZoneDNSNameRejectsLabelBoundary(t *testing.T) {
	svc := newDNSService(t)

	createZone(t, svc, &dns.ManagedZone{Name: "boundary-zone", DnsName: "example.com."})

	err := addRecord(t, svc, "boundary-zone", &dns.ResourceRecordSet{
		Name: "evilexample.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"},
	})
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("label-boundary-violating addition: got %v, want 400", err)
	}
}

// TestSDKAdditionAtZoneApexAndSubdomainAccepted is the negative control: a
// record set exactly at the zone's dnsName, and one on a genuine subdomain,
// must both be accepted.
func TestSDKAdditionAtZoneApexAndSubdomainAccepted(t *testing.T) {
	svc := newDNSService(t)

	createZone(t, svc, &dns.ManagedZone{Name: "valid-zone", DnsName: "valid.example.com."})

	if err := addRecord(t, svc, "valid-zone", &dns.ResourceRecordSet{
		Name: "valid.example.com.", Type: "TXT", Ttl: 300, Rrdatas: []string{`"apex"`},
	}); err != nil {
		t.Fatalf("apex addition rejected: %v", err)
	}

	if err := addRecord(t, svc, "valid-zone", &dns.ResourceRecordSet{
		Name: "www.valid.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"},
	}); err != nil {
		t.Fatalf("subdomain addition rejected: %v", err)
	}
}

// TestSDKNegativeTTLRejected guards the finding that a negative ttl was
// accepted; Cloud DNS TTLs are non-negative.
func TestSDKNegativeTTLRejected(t *testing.T) {
	svc := newDNSService(t)

	createZone(t, svc, &dns.ManagedZone{Name: "negttl-zone", DnsName: "negttl.example.com."})

	err := addRecord(t, svc, "negttl-zone", &dns.ResourceRecordSet{
		Name: "www.negttl.example.com.", Type: "A", Ttl: -5, Rrdatas: []string{"192.0.2.1"},
	})
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("negative ttl: got %v, want 400", err)
	}
}

// TestSDKZoneNameTooLongRejected guards the finding that Cloud DNS's 63-char
// managed-zone name cap was not enforced.
func TestSDKZoneNameTooLongRejected(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	_, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: strings.Repeat("a", 64), DnsName: "toolong.example.com.",
	}).Context(ctx).Do()
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("64-char zone name: got %v, want 400", err)
	}

	// A name at exactly the 63-char limit must still be accepted.
	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: strings.Repeat("a", 63), DnsName: "exactly63.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("63-char zone name rejected: %v", err)
	}
}

// TestSDKManagedZonesListDNSNameFilter guards the finding that
// managedZones.list ignored the ?dnsName= query filter and always returned
// every zone in the project.
func TestSDKManagedZonesListDNSNameFilter(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "dnf-a", DnsName: "dnf-a.example.com."})
	createZone(t, svc, &dns.ManagedZone{Name: "dnf-b", DnsName: "dnf-b.example.com."})

	list, err := svc.ManagedZones.List(testProject).DnsName("dnf-a.example.com.").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(dnsName filter): %v", err)
	}

	if len(list.ManagedZones) != 1 || list.ManagedZones[0].Name != "dnf-a" {
		t.Fatalf("dnsName filter = %+v, want exactly [dnf-a]", list.ManagedZones)
	}
}

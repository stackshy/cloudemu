package dns_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// TestSDKAzureDNSRecordSetFqdnNoTrailingDot asserts the record set fqdn matches
// real Azure, which returns "<name>.<zone>" for a relative record and "<zone>"
// for the apex — neither carrying a trailing dot (mirroring nameServers).
func TestSDKAzureDNSRecordSetFqdnNoTrailingDot(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "fqdn.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	rel, err := records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate: %v", err)
	}

	if rel.Properties == nil || rel.Properties.Fqdn == nil {
		t.Fatalf("record set fqdn missing: %+v", rel.Properties)
	}

	if got := *rel.Properties.Fqdn; got != "www."+zone {
		t.Fatalf("relative fqdn = %q, want %q (no trailing dot)", got, "www."+zone)
	}

	if strings.HasSuffix(*rel.Properties.Fqdn, ".") {
		t.Fatalf("relative fqdn = %q, want no trailing dot", *rel.Properties.Fqdn)
	}

	// The apex SOA record auto-provisioned with the zone reports the bare zone
	// name as its fqdn, again with no trailing dot.
	soa, err := records.Get(ctx, testRG, zone, "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get SOA: %v", err)
	}

	if soa.Properties == nil || soa.Properties.Fqdn == nil || *soa.Properties.Fqdn != zone {
		t.Fatalf("apex fqdn = %+v, want %q (no trailing dot)", soa.Properties, zone)
	}
}

// TestSDKAzureDNSDeleteZoneMissingIsIdempotent asserts that deleting a DNS zone
// that does not exist completes cleanly. Real Azure ARM DELETE is idempotent and
// returns 204 No Content ("The DNS zone was not found"), which lets the SDK LRO
// poller finish without a 404 error.
func TestSDKAzureDNSDeleteZoneMissingIsIdempotent(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	poller, err := zones.BeginDelete(ctx, testRG, "never-created.com", nil)
	if err != nil {
		t.Fatalf("Zones.BeginDelete on missing zone: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete of missing zone should be a no-op (204), got: %v", err)
	}
}

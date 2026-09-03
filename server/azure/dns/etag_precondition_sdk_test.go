package dns_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// TestSDKAzureDNSRecordSetETagLifecycle is the end-to-end regression for record-
// set ETag optimistic concurrency: a fresh zone already carries the auto SOA+NS
// with real etags, a plain CreateOrUpdate mints one, If-None-Match:"*" makes a
// second CreateOrUpdate at the same name create-only (412 once it already
// exists), a stale If-Match is rejected 412 without mutating the record, and the
// current etag succeeds and rotates to a new one.
func TestSDKAzureDNSRecordSetETagLifecycle(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "etag.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	// The auto-created apex SOA and NS already carry real, non-empty etags.
	list := records.NewListByDNSZonePager(testRG, zone, nil)

	var sawSOAEtag, sawNSEtag bool

	for list.More() {
		page, err := list.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByDNSZone: %v", err)
		}

		for _, rs := range page.Value {
			if rs.Etag == nil || *rs.Etag == "" {
				t.Fatalf("auto-created record set %+v has empty etag", rs)
			}

			switch {
			case rs.Type != nil && *rs.Type == "Microsoft.Network/dnsZones/SOA":
				sawSOAEtag = true
			case rs.Type != nil && *rs.Type == "Microsoft.Network/dnsZones/NS":
				sawNSEtag = true
			}
		}
	}

	if !sawSOAEtag || !sawNSEtag {
		t.Fatalf("expected auto SOA and NS record sets with etags, sawSOAEtag=%v sawNSEtag=%v", sawSOAEtag, sawNSEtag)
	}

	// A plain CreateOrUpdate (no precondition) mints an etag.
	created, err := records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate (initial): %v", err)
	}
	if created.Etag == nil || *created.Etag == "" {
		t.Fatal("created record set has empty etag")
	}

	firstEtag := *created.Etag

	// If-None-Match:"*" against an existing record set is rejected 412.
	_, err = records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.2")}},
		},
	}, &armdns.RecordSetsClientCreateOrUpdateOptions{IfNoneMatch: to.Ptr("*")})
	if !isStatus(err, 412) {
		t.Fatalf("create-only over an existing record set: got %v, want 412", err)
	}

	// A stale If-Match is rejected 412 and must not mutate the record.
	_, err = records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.3")}},
		},
	}, &armdns.RecordSetsClientCreateOrUpdateOptions{IfMatch: to.Ptr("not-the-real-etag")})
	if !isStatus(err, 412) {
		t.Fatalf("stale If-Match update: got %v, want 412", err)
	}

	unchanged, err := records.Get(ctx, testRG, zone, "www", armdns.RecordTypeA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get after rejected update: %v", err)
	}
	if unchanged.Etag == nil || *unchanged.Etag != firstEtag {
		t.Fatalf("etag changed despite rejected update: got %v, want %v", unchanged.Etag, firstEtag)
	}
	if len(unchanged.Properties.ARecords) != 1 || *unchanged.Properties.ARecords[0].IPv4Address != "192.0.2.1" {
		t.Fatalf("record value changed despite rejected update: %+v", unchanged.Properties.ARecords)
	}

	// The current etag succeeds and rotates to a new one.
	updated, err := records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.4")}},
		},
	}, &armdns.RecordSetsClientCreateOrUpdateOptions{IfMatch: to.Ptr(firstEtag)})
	if err != nil {
		t.Fatalf("update with current If-Match: %v", err)
	}
	if updated.Etag == nil || *updated.Etag == firstEtag {
		t.Fatalf("etag did not rotate on successful update: got %v", updated.Etag)
	}
	if len(updated.Properties.ARecords) != 1 || *updated.Properties.ARecords[0].IPv4Address != "192.0.2.4" {
		t.Fatalf("update did not apply: %+v", updated.Properties.ARecords)
	}
}

// TestSDKAzureDNSDeleteRecordSetIfMatch is the delete-side counterpart: a stale
// If-Match on RecordSets.Delete is rejected 412 and the record survives; the
// current etag deletes it.
func TestSDKAzureDNSDeleteRecordSetIfMatch(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "etag-delete.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	created, err := records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate: %v", err)
	}

	if _, err = records.Delete(ctx, testRG, zone, "www", armdns.RecordTypeA,
		&armdns.RecordSetsClientDeleteOptions{IfMatch: to.Ptr("stale-etag")}); !isStatus(err, 412) {
		t.Fatalf("delete with stale If-Match: got %v, want 412", err)
	}

	if _, err = records.Get(ctx, testRG, zone, "www", armdns.RecordTypeA, nil); err != nil {
		t.Fatalf("record should survive a rejected delete: %v", err)
	}

	if _, err = records.Delete(ctx, testRG, zone, "www", armdns.RecordTypeA,
		&armdns.RecordSetsClientDeleteOptions{IfMatch: created.Etag}); err != nil {
		t.Fatalf("delete with current If-Match: %v", err)
	}

	if _, err = records.Get(ctx, testRG, zone, "www", armdns.RecordTypeA, nil); !isStatus(err, 404) {
		t.Fatalf("record should be gone: got %v, want 404", err)
	}
}

// TestSDKAzureDNSCreateOrUpdateIfNoneMatchNewRecord asserts If-None-Match:"*"
// succeeds — as a plain create — the first time a record set is written, only
// failing once one already exists at that name+type.
func TestSDKAzureDNSCreateOrUpdateIfNoneMatchNewRecord(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "etag-create.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	created, err := records.CreateOrUpdate(ctx, testRG, zone, "fresh", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.9")}},
		},
	}, &armdns.RecordSetsClientCreateOrUpdateOptions{IfNoneMatch: to.Ptr("*")})
	if err != nil {
		t.Fatalf("create-only against an absent record set: %v", err)
	}
	if created.Etag == nil || *created.Etag == "" {
		t.Fatal("created record set has empty etag")
	}
}

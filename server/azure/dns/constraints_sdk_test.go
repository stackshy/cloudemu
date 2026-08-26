package dns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// TestSDKAzureDNSMaxNumberOfRecordSets asserts a public zone reports the real
// Azure default record-set cap (10000), not the old 5000.
func TestSDKAzureDNSMaxNumberOfRecordSets(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	const zone = "cap.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	got, err := zones.Get(ctx, testRG, zone, nil)
	if err != nil {
		t.Fatalf("Zones.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.MaxNumberOfRecordSets == nil {
		t.Fatalf("maxNumberOfRecordSets missing: %+v", got.Properties)
	}

	if v := *got.Properties.MaxNumberOfRecordSets; v != 10000 {
		t.Fatalf("maxNumberOfRecordSets = %d, want 10000", v)
	}
}

// TestSDKAzureDNSListByType asserts RecordSets.ListByType returns the zone's
// record sets of the requested type — previously a type-only GET fell through
// to a single-record Get and 404'd.
func TestSDKAzureDNSListByType(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "bytype.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	for _, name := range []string{"www", "api"} {
		if _, err := records.CreateOrUpdate(ctx, testRG, zone, name, armdns.RecordTypeA, armdns.RecordSet{
			Properties: &armdns.RecordSetProperties{
				TTL:      to.Ptr(int64(300)),
				ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
			},
		}, nil); err != nil {
			t.Fatalf("RecordSets.CreateOrUpdate %s: %v", name, err)
		}
	}

	pager := records.NewListByTypePager(testRG, zone, armdns.RecordTypeA, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByType(A): %v", err)
		}

		for _, rs := range page.Value {
			if rs.Type == nil || *rs.Type != "Microsoft.Network/dnsZones/A" {
				t.Errorf("ListByType(A) returned a non-A record set: %+v", rs.Type)
			}

			names = append(names, *rs.Name)
		}
	}

	if !contains(names, "www") || !contains(names, "api") {
		t.Fatalf("ListByType(A) = %v, want to contain www and api", names)
	}
}

// TestSDKAzureDNSCNAMECoexistence asserts Azure's CNAME coexistence rule in
// both orders: a CNAME cannot be added where another type exists, and another
// type cannot be added where a CNAME exists. Both must fail with 400.
func TestSDKAzureDNSCNAMECoexistence(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "coexist.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	aSet := armdns.RecordSet{Properties: &armdns.RecordSetProperties{
		TTL:      to.Ptr(int64(300)),
		ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
	}}
	cnameSet := armdns.RecordSet{Properties: &armdns.RecordSetProperties{
		TTL:         to.Ptr(int64(300)),
		CnameRecord: &armdns.CnameRecord{Cname: to.Ptr("target.example.com")},
	}}

	// A exists at "shop" → adding a CNAME at "shop" is rejected.
	if _, err := records.CreateOrUpdate(ctx, testRG, zone, "shop", armdns.RecordTypeA, aSet, nil); err != nil {
		t.Fatalf("seed A shop: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, zone, "shop", armdns.RecordTypeCNAME, cnameSet, nil); !isStatus(err, 400) {
		t.Fatalf("CNAME over existing A: got %v, want 400", err)
	}

	// CNAME exists at "blog" → adding an A at "blog" is rejected.
	if _, err := records.CreateOrUpdate(ctx, testRG, zone, "blog", armdns.RecordTypeCNAME, cnameSet, nil); err != nil {
		t.Fatalf("seed CNAME blog: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, zone, "blog", armdns.RecordTypeA, aSet, nil); !isStatus(err, 400) {
		t.Fatalf("A over existing CNAME: got %v, want 400", err)
	}
}

// TestSDKAzureDNSDeleteApexSOAProtected asserts the auto-provisioned apex SOA
// record set cannot be deleted, matching Azure.
func TestSDKAzureDNSDeleteApexSOAProtected(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "apex.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if _, err := records.Delete(ctx, testRG, zone, "@", armdns.RecordTypeSOA, nil); !isStatus(err, 400) {
		t.Fatalf("Delete apex SOA: got %v, want 400", err)
	}

	// The apex NS record set is likewise protected.
	if _, err := records.Delete(ctx, testRG, zone, "@", armdns.RecordTypeNS, nil); !isStatus(err, 400) {
		t.Fatalf("Delete apex NS: got %v, want 400", err)
	}
}

// isStatus reports whether err is an ARM response error carrying the given
// HTTP status code.
func isStatus(err error, code int) bool {
	var respErr *azcore.ResponseError

	return errors.As(err, &respErr) && respErr.StatusCode == code
}

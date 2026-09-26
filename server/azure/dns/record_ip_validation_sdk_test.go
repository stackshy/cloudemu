package dns_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// requireBadRequest fails unless err is a 400 BadRequest with the given message.
func requireBadRequest(t *testing.T, err error, wantMsg string) {
	t.Helper()

	var rerr *azcore.ResponseError
	if !errors.As(err, &rerr) || rerr.StatusCode != http.StatusBadRequest || rerr.ErrorCode != "BadRequest" {
		t.Fatalf("got %v, want 400 BadRequest", err)
	}

	if wantMsg != "" && !strings.Contains(rerr.Error(), wantMsg) {
		t.Fatalf("error %q does not contain %q", rerr.Error(), wantMsg)
	}
}

// TestSDKAzureDNSRejectsBadAddress checks that an A or AAAA record set with a
// value that is not an address of the right family is rejected as 400
// BadRequest. The record set is one resource, so a single bad value means
// nothing is stored.
func TestSDKAzureDNSRejectsBadAddress(t *testing.T) {
	tests := []struct {
		name    string
		rtype   armdns.RecordType
		props   *armdns.RecordSetProperties
		wantMsg string
	}{
		{
			name:  "A not an IP",
			rtype: armdns.RecordTypeA,
			props: &armdns.RecordSetProperties{TTL: to.Ptr(int64(300)), ARecords: []*armdns.ARecord{
				{IPv4Address: to.Ptr("192.0.2.1")}, {IPv4Address: to.Ptr("999.1.1.1")},
			}},
			wantMsg: "The value '999.1.1.1' of field 'ipv4Address' is not a valid IPv4 address.",
		},
		{
			name:    "A missing address",
			rtype:   armdns.RecordTypeA,
			props:   &armdns.RecordSetProperties{TTL: to.Ptr(int64(300)), ARecords: []*armdns.ARecord{{}}},
			wantMsg: "The resource record is missing field 'ipv4Address'.",
		},
		{
			name:  "AAAA holding IPv4",
			rtype: armdns.RecordTypeAAAA,
			props: &armdns.RecordSetProperties{TTL: to.Ptr(int64(300)), AaaaRecords: []*armdns.AaaaRecord{
				{IPv6Address: to.Ptr("192.0.2.1")},
			}},
			wantMsg: "The value '192.0.2.1' of field 'ipv6Address' is not a valid IPv6 address.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zones, records := newDNSClients(t)
			ctx := context.Background()

			const zone = "badip.com"

			if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
				t.Fatalf("Zones.CreateOrUpdate: %v", err)
			}

			_, err := records.CreateOrUpdate(ctx, testRG, zone, "www", tt.rtype, armdns.RecordSet{Properties: tt.props}, nil)
			requireBadRequest(t, err, tt.wantMsg)

			if _, gerr := records.Get(ctx, testRG, zone, "www", tt.rtype, nil); gerr == nil {
				t.Fatal("record set stored although the PUT was rejected")
			}
		})
	}
}

// TestSDKAzureDNSPatchRejectsBadAddress checks that a PATCH with a bad A value
// is rejected and leaves the stored values alone. A good PUT is accepted.
func TestSDKAzureDNSPatchRejectsBadAddress(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	const zone = "patchip.com"

	if _, err := zones.CreateOrUpdate(ctx, testRG, zone, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil); err != nil {
		t.Fatalf("good PUT: %v", err)
	}

	_, err := records.Update(ctx, testRG, zone, "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("not-an-ip")}}},
	}, nil)
	requireBadRequest(t, err, "The value 'not-an-ip' of field 'ipv4Address' is not a valid IPv4 address.")

	got, err := records.Get(ctx, testRG, zone, "www", armdns.RecordTypeA, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Properties.ARecords) != 1 || *got.Properties.ARecords[0].IPv4Address != "192.0.2.1" {
		t.Fatalf("stored values changed by a rejected PATCH: %+v", got.Properties.ARecords)
	}
}

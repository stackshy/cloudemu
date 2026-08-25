package dns_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG  = "rg-1"
	testSub = "sub-1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newDNSClients(t *testing.T) (*armdns.ZonesClient, *armdns.RecordSetsClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{DNS: cloudP.DNS})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armdns.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armdns.NewClientFactory: %v", err)
	}

	return cf.NewZonesClient(), cf.NewRecordSetsClient()
}

func TestSDKAzureDNSZoneLifecycle(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	created, err := zones.CreateOrUpdate(ctx, testRG, "example.com", armdns.Zone{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if created.Name == nil || *created.Name != "example.com" {
		t.Fatalf("CreateOrUpdate name = %v, want example.com", created.Name)
	}

	if created.Properties == nil || created.Properties.NumberOfRecordSets == nil ||
		*created.Properties.NumberOfRecordSets != 2 {
		t.Fatalf("numberOfRecordSets = %+v, want 2 (auto apex SOA+NS)", created.Properties)
	}

	if created.Properties == nil || len(created.Properties.NameServers) != 4 {
		t.Fatalf("nameServers = %+v, want 4 authoritative name servers", created.Properties)
	}

	for _, ns := range created.Properties.NameServers {
		if ns == nil || *ns == "" {
			t.Fatalf("nameServer entry empty, want e.g. ns1-01.azure-dns.com")
		}

		if (*ns)[len(*ns)-1] == '.' {
			t.Fatalf("nameServer = %q, want no trailing dot (real Azure returns ns1-01.azure-dns.com)", *ns)
		}
	}

	got, err := zones.Get(ctx, testRG, "example.com", nil)
	if err != nil {
		t.Fatalf("Zones.Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	if got.Etag == nil || *got.Etag == "" {
		t.Fatalf("zone etag = %v, want non-empty", got.Etag)
	}

	var names []string

	pager := zones.NewListByResourceGroupPager(testRG, nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByResourceGroup: %v", perr)
		}

		for _, z := range page.Value {
			names = append(names, *z.Name)
		}
	}

	if len(names) != 1 || names[0] != "example.com" {
		t.Fatalf("list = %v, want [example.com]", names)
	}

	poller, err := zones.BeginDelete(ctx, testRG, "example.com", nil)
	if err != nil {
		t.Fatalf("Zones.BeginDelete: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = zones.Get(ctx, testRG, "example.com", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKAzureDNSRecordSets(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "records.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	set, err := records.CreateOrUpdate(ctx, testRG, "records.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate: %v", err)
	}

	if set.Name == nil || *set.Name != "www" {
		t.Fatalf("record set name = %v, want www", set.Name)
	}

	got, err := records.Get(ctx, testRG, "records.com", "www", armdns.RecordTypeA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.TTL == nil || *got.Properties.TTL != 300 {
		t.Fatalf("TTL = %+v, want 300", got.Properties)
	}

	if len(got.Properties.ARecords) != 1 || *got.Properties.ARecords[0].IPv4Address != "192.0.2.1" {
		t.Fatalf("ARecords = %+v, want [192.0.2.1]", got.Properties.ARecords)
	}

	if got.Properties.Fqdn == nil || *got.Properties.Fqdn != "www.records.com" {
		t.Fatalf("fqdn = %v, want www.records.com (no trailing dot)", got.Properties.Fqdn)
	}

	var listed []string

	pager := records.NewListByDNSZonePager(testRG, "records.com", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByDNSZone: %v", perr)
		}

		for _, rs := range page.Value {
			listed = append(listed, *rs.Name)
		}
	}

	// The zone auto-provisions apex SOA and NS record sets, so the list carries
	// them alongside the user's www record.
	if !contains(listed, "www") || !contains(listed, "@") {
		t.Fatalf("record list = %v, want to contain www and apex @ (SOA/NS)", listed)
	}

	if _, err := records.Delete(ctx, testRG, "records.com", "www", armdns.RecordTypeA, nil); err != nil {
		t.Fatalf("RecordSets.Delete: %v", err)
	}

	_, err = records.Get(ctx, testRG, "records.com", "www", armdns.RecordTypeA, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

// TestSDKAzureDNSListPreservesUnmodeledRecordTypes is the regression for the
// HIGH finding: MX and SRV record sets are not natively modeled by the DNS
// handler (Azure represents them as properties.MXRecords / properties.SRVRecords
// — see RecordSet in
// https://learn.microsoft.com/en-us/rest/api/dns/record-sets/list-by-dns-zone),
// so cloudemu relies on the unmodeled-property overlay to round-trip their
// data. A single RecordSets.Get on one of them worked, but
// RecordSets.ListByDnsZone returned every record set with its MXRecords /
// SRVRecords silently dropped. A/AAAA/CNAME/TXT are natively modeled and must
// keep listing correctly alongside the fix.
func TestSDKAzureDNSListPreservesUnmodeledRecordTypes(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "unmodeled.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "unmodeled.com", "@", armdns.RecordTypeMX, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL: to.Ptr(int64(300)),
			MxRecords: []*armdns.MxRecord{
				{Exchange: to.Ptr("mail.unmodeled.com"), Preference: to.Ptr(int32(10))},
			},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(MX): %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "unmodeled.com", "_sip._tcp", armdns.RecordTypeSRV, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL: to.Ptr(int64(300)),
			SrvRecords: []*armdns.SrvRecord{
				{Priority: to.Ptr(int32(1)), Weight: to.Ptr(int32(5)), Port: to.Ptr(int32(5060)), Target: to.Ptr("sip.unmodeled.com")},
			},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(SRV): %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "unmodeled.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("203.0.113.9")}},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(A): %v", err)
	}

	var (
		mxSeen, srvSeen, aSeen bool
	)

	pager := records.NewListByDNSZonePager(testRG, "unmodeled.com", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByDNSZone: %v", perr)
		}

		for _, rs := range page.Value {
			if rs.Properties == nil {
				continue
			}

			switch {
			case len(rs.Properties.MxRecords) > 0:
				mxSeen = true

				mx := rs.Properties.MxRecords[0]
				if mx.Exchange == nil || *mx.Exchange != "mail.unmodeled.com" || mx.Preference == nil || *mx.Preference != 10 {
					t.Errorf("list MX record = %+v, want exchange=mail.unmodeled.com preference=10", mx)
				}
			case len(rs.Properties.SrvRecords) > 0:
				srvSeen = true

				srv := rs.Properties.SrvRecords[0]
				if srv.Target == nil || *srv.Target != "sip.unmodeled.com" || srv.Port == nil || *srv.Port != 5060 {
					t.Errorf("list SRV record = %+v, want target=sip.unmodeled.com port=5060", srv)
				}
			case len(rs.Properties.ARecords) > 0:
				aSeen = true

				if *rs.Properties.ARecords[0].IPv4Address != "203.0.113.9" {
					t.Errorf("list A record = %+v, want 203.0.113.9", rs.Properties.ARecords[0])
				}
			}
		}
	}

	if !mxSeen {
		t.Error("ListByDnsZone dropped the MX record set's MXRecords data")
	}

	if !srvSeen {
		t.Error("ListByDnsZone dropped the SRV record set's SRVRecords data")
	}

	if !aSeen {
		t.Error("ListByDnsZone regressed on the natively-modeled A record set")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

func TestSDKAzureDNSErrors(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	_, err := zones.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}

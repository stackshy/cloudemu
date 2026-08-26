package dns_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// statusRecorder is a per-call ARM pipeline policy that remembers the HTTP
// status code of the most recent response, so a test can assert on the exact
// 200-vs-201 status the handler returned (the typed SDK responses do not expose
// it).
type statusRecorder struct{ last int }

func (s *statusRecorder) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp != nil {
		s.last = resp.StatusCode
	}

	return resp, err
}

func newDNSClientsWithStatus(t *testing.T) (*armdns.ZonesClient, *armdns.RecordSetsClient, *statusRecorder) {
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

	rec := &statusRecorder{}
	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:           myCloud,
			Transport:       ts.Client(),
			Retry:           policy.RetryOptions{MaxRetries: -1},
			PerCallPolicies: []policy.Policy{rec},
		},
	}

	cf, err := armdns.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armdns.NewClientFactory: %v", err)
	}

	return cf.NewZonesClient(), cf.NewRecordSetsClient(), rec
}

// TestSDKAzureDNSEditApexSOA is the BUG1 regression: a caller-edited apex SOA
// record set must read back the edited timing fields (refreshTime, minimumTTL,
// …) rather than being re-stamped with the platform defaults on every read.
func TestSDKAzureDNSEditApexSOA(t *testing.T) {
	zones, records, _ := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "soa.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	// The auto-created apex SOA starts at Azure's platform defaults.
	got, err := records.Get(ctx, testRG, "soa.com", "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get(SOA): %v", err)
	}

	soa := got.Properties.SoaRecord
	if soa == nil || soa.RefreshTime == nil || *soa.RefreshTime != 3600 || soa.MinimumTTL == nil || *soa.MinimumTTL != 300 {
		t.Fatalf("auto SOA = %+v, want default refreshTime=3600 minimumTTL=300", soa)
	}

	host := *soa.Host

	// Edit the timing fields (real clients GET then PUT the whole record set).
	if _, err := records.CreateOrUpdate(ctx, testRG, "soa.com", "@", armdns.RecordTypeSOA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL: to.Ptr(int64(3600)),
			SoaRecord: &armdns.SoaRecord{
				Host:         to.Ptr(host),
				RefreshTime:  to.Ptr(int64(7200)),
				MinimumTTL:   to.Ptr(int64(600)),
				ExpireTime:   to.Ptr(int64(1209600)),
				SerialNumber: to.Ptr(int64(42)),
			},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(SOA edit): %v", err)
	}

	edited, err := records.Get(ctx, testRG, "soa.com", "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get(SOA after edit): %v", err)
	}

	e := edited.Properties.SoaRecord
	if e == nil || e.RefreshTime == nil || *e.RefreshTime != 7200 {
		t.Fatalf("edited SOA refreshTime = %+v, want 7200", e)
	}
	if e.MinimumTTL == nil || *e.MinimumTTL != 600 {
		t.Fatalf("edited SOA minimumTTL = %+v, want 600", e.MinimumTTL)
	}
	if e.ExpireTime == nil || *e.ExpireTime != 1209600 {
		t.Fatalf("edited SOA expireTime = %+v, want 1209600", e.ExpireTime)
	}
	if e.SerialNumber == nil || *e.SerialNumber != 42 {
		t.Fatalf("edited SOA serialNumber = %+v, want 42", e.SerialNumber)
	}
	// retryTime was not edited, so it must keep the platform default.
	if e.RetryTime == nil || *e.RetryTime != 300 {
		t.Fatalf("edited SOA retryTime = %+v, want default 300 (unedited)", e.RetryTime)
	}
	// host is read-only and must be unchanged.
	if e.Host == nil || *e.Host != host {
		t.Fatalf("edited SOA host = %+v, want unchanged %q", e.Host, host)
	}
}

// TestSDKAzureDNSPatchSOATimingPreservesHost is the residue regression: a PATCH
// (RecordSets.Update) that supplies ONLY a timing field on the apex SOA — with
// host and email omitted — must keep the system-managed host and email stable
// (recordValues would otherwise yield ["",""] and wipe them) while applying the
// new timing value.
func TestSDKAzureDNSPatchSOATimingPreservesHost(t *testing.T) {
	zones, records, _ := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "soapatch.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	before, err := records.Get(ctx, testRG, "soapatch.com", "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get(SOA): %v", err)
	}

	host := *before.Properties.SoaRecord.Host
	email := *before.Properties.SoaRecord.Email

	// PATCH ONLY refreshTime; host and email are omitted from the body.
	patched, err := records.Update(ctx, testRG, "soapatch.com", "@", armdns.RecordTypeSOA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			SoaRecord: &armdns.SoaRecord{RefreshTime: to.Ptr(int64(9999))},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.Update(timing only): %v", err)
	}

	p := patched.Properties.SoaRecord
	if p == nil || p.Host == nil || *p.Host != host {
		t.Fatalf("PATCH timing-only SOA host = %+v, want unchanged %q", p, host)
	}
	if p.Email == nil || *p.Email != email {
		t.Fatalf("PATCH timing-only SOA email = %+v, want unchanged %q", p.Email, email)
	}
	if p.RefreshTime == nil || *p.RefreshTime != 9999 {
		t.Fatalf("PATCH timing-only SOA refreshTime = %+v, want 9999", p.RefreshTime)
	}

	// Read-back confirms the host/email survived the write, not just the response.
	after, err := records.Get(ctx, testRG, "soapatch.com", "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get(SOA after patch): %v", err)
	}

	a := after.Properties.SoaRecord
	if a.Host == nil || *a.Host != host || a.Email == nil || *a.Email != email {
		t.Fatalf("read-back SOA host/email = %v/%v, want %q/%q", a.Host, a.Email, host, email)
	}
	if a.RefreshTime == nil || *a.RefreshTime != 9999 {
		t.Fatalf("read-back SOA refreshTime = %+v, want 9999", a.RefreshTime)
	}
}

// TestSDKAzureDNSAutoSOADefaults asserts that a freshly-created zone's
// auto-provisioned apex SOA still reads back Azure's platform defaults when it
// has not been edited (the BUG1 fix must not disturb the default path).
func TestSDKAzureDNSAutoSOADefaults(t *testing.T) {
	zones, records, _ := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "default-soa.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	got, err := records.Get(ctx, testRG, "default-soa.com", "@", armdns.RecordTypeSOA, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get(SOA): %v", err)
	}

	soa := got.Properties.SoaRecord
	switch {
	case soa == nil:
		t.Fatalf("SOA record missing")
	case *soa.RefreshTime != 3600:
		t.Fatalf("refreshTime = %d, want 3600", *soa.RefreshTime)
	case *soa.RetryTime != 300:
		t.Fatalf("retryTime = %d, want 300", *soa.RetryTime)
	case *soa.ExpireTime != 2419200:
		t.Fatalf("expireTime = %d, want 2419200", *soa.ExpireTime)
	case *soa.MinimumTTL != 300:
		t.Fatalf("minimumTTL = %d, want 300", *soa.MinimumTTL)
	case *soa.SerialNumber != 1:
		t.Fatalf("serialNumber = %d, want 1", *soa.SerialNumber)
	}
}

// TestSDKAzureDNSPatchZoneTagsMerge is the GAP-A regression for zones: a PATCH
// (Zones.Update) must merge the supplied tags over the zone's existing tags,
// preserving tags the caller did not resend, and return 200.
func TestSDKAzureDNSPatchZoneTagsMerge(t *testing.T) {
	zones, _, _ := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "patchzone.com", armdns.Zone{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	updated, err := zones.Update(ctx, testRG, "patchzone.com", armdns.ZoneUpdate{
		Tags: map[string]*string{"team": to.Ptr("blue")},
	}, nil)
	if err != nil {
		t.Fatalf("Zones.Update: %v", err)
	}

	if updated.Tags["env"] == nil || *updated.Tags["env"] != "test" {
		t.Fatalf("after PATCH tags = %v, want env=test preserved", updated.Tags)
	}
	if updated.Tags["team"] == nil || *updated.Tags["team"] != "blue" {
		t.Fatalf("after PATCH tags = %v, want team=blue added", updated.Tags)
	}

	// A read-back confirms the merge persisted, not just the response body.
	got, err := zones.Get(ctx, testRG, "patchzone.com", nil)
	if err != nil {
		t.Fatalf("Zones.Get: %v", err)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "test" || got.Tags["team"] == nil || *got.Tags["team"] != "blue" {
		t.Fatalf("read-back tags = %v, want env=test and team=blue", got.Tags)
	}
}

// TestSDKAzureDNSPatchRecordSetPreservesOmitted is the GAP-A regression for
// record sets: a PATCH (RecordSets.Update) must merge the supplied fields and
// preserve any field the caller omitted (no nil-masking).
func TestSDKAzureDNSPatchRecordSetPreservesOmitted(t *testing.T) {
	zones, records, _ := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "patchrec.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "patchrec.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL: to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{
				{IPv4Address: to.Ptr("192.0.2.1")},
				{IPv4Address: to.Ptr("192.0.2.2")},
			},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate: %v", err)
	}

	// PATCH only the TTL; the A records must be preserved.
	patched, err := records.Update(ctx, testRG, "patchrec.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{TTL: to.Ptr(int64(600))},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.Update(ttl only): %v", err)
	}

	if patched.Properties.TTL == nil || *patched.Properties.TTL != 600 {
		t.Fatalf("PATCH TTL = %+v, want 600", patched.Properties.TTL)
	}
	if len(patched.Properties.ARecords) != 2 {
		t.Fatalf("PATCH dropped A records: %+v, want 2 preserved", patched.Properties.ARecords)
	}

	// PATCH only the records; the TTL just set must be preserved.
	patched2, err := records.Update(ctx, testRG, "patchrec.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("203.0.113.7")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("RecordSets.Update(records only): %v", err)
	}

	if patched2.Properties.TTL == nil || *patched2.Properties.TTL != 600 {
		t.Fatalf("PATCH2 TTL = %+v, want 600 preserved", patched2.Properties.TTL)
	}
	if len(patched2.Properties.ARecords) != 1 || *patched2.Properties.ARecords[0].IPv4Address != "203.0.113.7" {
		t.Fatalf("PATCH2 A records = %+v, want [203.0.113.7]", patched2.Properties.ARecords)
	}
}

// TestSDKAzureDNSRecordSetCreateUpdateStatus is the BUG2 regression:
// RecordSets.CreateOrUpdate returns 201 Created on first create and 200 OK when
// updating an existing record set.
func TestSDKAzureDNSRecordSetCreateUpdateStatus(t *testing.T) {
	zones, records, rec := newDNSClientsWithStatus(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "status.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "status.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.1")}},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(create): %v", err)
	}
	if rec.last != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rec.last)
	}

	if _, err := records.CreateOrUpdate(ctx, testRG, "status.com", "www", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(600)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("192.0.2.9")}},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate(update): %v", err)
	}
	if rec.last != http.StatusOK {
		t.Fatalf("update status = %d, want 200", rec.last)
	}
}

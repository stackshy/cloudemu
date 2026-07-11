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

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
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

	got, err := zones.Get(ctx, testRG, "example.com", nil)
	if err != nil {
		t.Fatalf("Zones.Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
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

	if len(listed) != 1 || listed[0] != "www" {
		t.Fatalf("record list = %v, want [www]", listed)
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

func TestSDKAzureDNSErrors(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	_, err := zones.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}

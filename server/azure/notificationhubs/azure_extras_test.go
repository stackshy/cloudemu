package notificationhubs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newFactory(t *testing.T) *armnotificationhubs.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{NotificationHubs: cloudP.NotificationHubs})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armnotificationhubs.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return cf
}

func mkNamespace(t *testing.T, ns *armnotificationhubs.NamespacesClient, rg, name, location string, s armnotificationhubs.SKUName) {
	t.Helper()

	_, err := ns.CreateOrUpdate(context.Background(), rg, name, armnotificationhubs.NamespaceCreateOrUpdateParameters{
		Location: to.Ptr(location),
		SKU:      &armnotificationhubs.SKU{Name: to.Ptr(s)},
	}, nil)
	if err != nil {
		t.Fatalf("namespace CreateOrUpdate: %v", err)
	}
}

func TestSDKNamespaceSKUAndLocation(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	mkNamespace(t, ns, testRG, "my-ns", "eastus", armnotificationhubs.SKUNameStandard)

	got, err := ns.Get(ctx, testRG, "my-ns", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armnotificationhubs.SKUNameStandard {
		t.Fatalf("sku = %+v, want Standard", got.SKU)
	}

	if got.Location == nil || *got.Location != "eastus" {
		t.Fatalf("location = %v, want eastus", got.Location)
	}

	if got.Properties == nil || got.Properties.ServiceBusEndpoint == nil ||
		!strings.Contains(*got.Properties.ServiceBusEndpoint, "my-ns.servicebus.windows.net") {
		t.Fatalf("serviceBusEndpoint = %+v", got.Properties)
	}

	if got.Properties.NamespaceType == nil || *got.Properties.NamespaceType != armnotificationhubs.NamespaceTypeNotificationHub {
		t.Fatalf("namespaceType = %v", got.Properties.NamespaceType)
	}
}

func TestSDKNamespaceAuthorizationRuleAndKeys(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	mkNamespace(t, ns, testRG, "my-ns", "eastus", armnotificationhubs.SKUNameStandard)

	rights := []*armnotificationhubs.AccessRights{
		to.Ptr(armnotificationhubs.AccessRightsListen),
		to.Ptr(armnotificationhubs.AccessRightsSend),
	}

	created, err := ns.CreateOrUpdateAuthorizationRule(ctx, testRG, "my-ns", "sender",
		armnotificationhubs.SharedAccessAuthorizationRuleCreateOrUpdateParameters{
			Properties: &armnotificationhubs.SharedAccessAuthorizationRuleProperties{Rights: rights},
		}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAuthorizationRule: %v", err)
	}

	if created.Name == nil || *created.Name != "sender" {
		t.Fatalf("rule name = %v, want sender", created.Name)
	}

	got, err := ns.GetAuthorizationRule(ctx, testRG, "my-ns", "sender", nil)
	if err != nil {
		t.Fatalf("GetAuthorizationRule: %v", err)
	}

	if got.Properties == nil || len(got.Properties.Rights) != 2 {
		t.Fatalf("rights = %+v, want 2", got.Properties)
	}

	keys, err := ns.ListKeys(ctx, testRG, "my-ns", "sender", nil)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	if keys.PrimaryKey == nil || *keys.PrimaryKey == "" ||
		keys.PrimaryConnectionString == nil || !strings.Contains(*keys.PrimaryConnectionString, "SharedAccessKey=") {
		t.Fatalf("keys = %+v", keys)
	}
}

// TestSDKNamespaceDefaultRuleKeys verifies ListKeys works on the auto-created
// RootManageSharedAccessKey without an explicit CreateOrUpdate.
func TestSDKNamespaceDefaultRuleKeys(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	mkNamespace(t, ns, testRG, "my-ns", "eastus", armnotificationhubs.SKUNameStandard)

	keys, err := ns.ListKeys(ctx, testRG, "my-ns", "RootManageSharedAccessKey", nil)
	if err != nil {
		t.Fatalf("ListKeys(RootManageSharedAccessKey): %v", err)
	}

	if keys.PrimaryKey == nil || *keys.PrimaryKey == "" {
		t.Fatalf("primary key missing: %+v", keys)
	}
}

func TestSDKHubAuthorizationRuleKeys(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	hubs := cf.NewClient()
	ctx := context.Background()

	mkNamespace(t, ns, testRG, "my-ns", "eastus", armnotificationhubs.SKUNameStandard)

	if _, err := hubs.CreateOrUpdate(ctx, testRG, "my-ns", "hub1",
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("hub CreateOrUpdate: %v", err)
	}

	if _, err := hubs.CreateOrUpdateAuthorizationRule(ctx, testRG, "my-ns", "hub1", "listener",
		armnotificationhubs.SharedAccessAuthorizationRuleCreateOrUpdateParameters{
			Properties: &armnotificationhubs.SharedAccessAuthorizationRuleProperties{
				Rights: []*armnotificationhubs.AccessRights{to.Ptr(armnotificationhubs.AccessRightsListen)},
			},
		}, nil); err != nil {
		t.Fatalf("hub CreateOrUpdateAuthorizationRule: %v", err)
	}

	keys, err := hubs.ListKeys(ctx, testRG, "my-ns", "hub1", "listener", nil)
	if err != nil {
		t.Fatalf("hub ListKeys: %v", err)
	}

	if keys.PrimaryConnectionString == nil || !strings.Contains(*keys.PrimaryConnectionString, "my-ns.servicebus.windows.net") {
		t.Fatalf("hub keys = %+v", keys)
	}
}

func TestSDKCheckAvailability(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	res, err := ns.CheckAvailability(ctx, armnotificationhubs.CheckAvailabilityParameters{
		Name: to.Ptr("fresh-name"),
	}, nil)
	if err != nil {
		t.Fatalf("CheckAvailability: %v", err)
	}

	if res.IsAvailiable == nil || !*res.IsAvailiable {
		t.Fatalf("IsAvailiable = %v, want true", res.IsAvailiable)
	}
}

func TestSDKListAllPreservesResourceGroup(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	mkNamespace(t, ns, "rg-alpha", "ns-alpha", "eastus", armnotificationhubs.SKUNameStandard)

	var ids []string

	pager := ns.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}

		for _, n := range page.Value {
			ids = append(ids, *n.ID)
		}
	}

	if len(ids) != 1 {
		t.Fatalf("ListAll returned %d namespaces, want 1", len(ids))
	}

	if !strings.Contains(ids[0], "/resourceGroups/rg-alpha/") || strings.Contains(ids[0], "resourceGroups//") {
		t.Fatalf("id = %q, want a well-formed rg-alpha id", ids[0])
	}
}

func TestSDKGetWrongResourceGroup(t *testing.T) {
	cf := newFactory(t)
	ns := cf.NewNamespacesClient()
	ctx := context.Background()

	mkNamespace(t, ns, "rg-1", "ns1", "eastus", armnotificationhubs.SKUNameStandard)

	_, err := ns.Get(ctx, "rg-2", "ns1", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("err = %v, want 404", err)
	}
}

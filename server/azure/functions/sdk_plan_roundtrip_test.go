package functions_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newPlansClient(t *testing.T, ts *httptest.Server) *armappservice.PlansClient {
	t.Helper()

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

	clientFactory, err := armappservice.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return clientFactory.NewPlansClient()
}

func TestSDKAzureAppServicePlanCreateGet(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Functions: cloudP.Functions})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newPlansClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-plan",
		armappservice.Plan{
			Kind:     to.Ptr("linux"),
			Location: to.Ptr("eastus"),
			SKU: &armappservice.SKUDescription{
				Name:     to.Ptr("P1v3"),
				Tier:     to.Ptr("PremiumV3"),
				Capacity: to.Ptr[int32](3),
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, &runtimePollerOptions)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	assertPlan(t, created.Plan, "create")

	got, err := client.Get(ctx, rgName, "sdk-plan", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertPlan(t, got.Plan, "get")
}

func assertPlan(t *testing.T, p armappservice.Plan, stage string) {
	t.Helper()

	if p.Name == nil || *p.Name != "sdk-plan" {
		t.Fatalf("%s Name = %v, want sdk-plan", stage, p.Name)
	}

	if p.Kind == nil || *p.Kind != "linux" {
		t.Fatalf("%s Kind = %v, want linux", stage, p.Kind)
	}

	if p.SKU == nil {
		t.Fatalf("%s SKU is nil", stage)
	}

	if p.SKU.Name == nil || *p.SKU.Name != "P1v3" {
		t.Fatalf("%s SKU.Name = %v, want P1v3", stage, p.SKU.Name)
	}

	if p.SKU.Tier == nil || *p.SKU.Tier != "PremiumV3" {
		t.Fatalf("%s SKU.Tier = %v, want PremiumV3", stage, p.SKU.Tier)
	}

	if p.SKU.Capacity == nil || *p.SKU.Capacity != 3 {
		t.Fatalf("%s SKU.Capacity = %v, want 3", stage, p.SKU.Capacity)
	}
}

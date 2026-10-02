package loganalytics_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

// TestSDKDeletedWorkspacesListEmpty pins the deletedWorkspaces lists azurerm
// reads on every workspace create: 200 with no entries, never 501.
func TestSDKDeletedWorkspacesListEmpty(t *testing.T) {
	_, ts := newWorkspacesClient(t)
	ctx := context.Background()

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armoperationalinsights.NewDeletedWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	sub, err := client.NewListPager(nil).NextPage(ctx)
	if err != nil || len(sub.Value) != 0 {
		t.Fatalf("subscription list: err=%v len=%d, want empty", err, len(sub.Value))
	}

	rg, err := client.NewListByResourceGroupPager(testRG, nil).NextPage(ctx)
	if err != nil || len(rg.Value) != 0 {
		t.Fatalf("resource group list: err=%v len=%d, want empty", err, len(rg.Value))
	}
}

func putWorkspace(t *testing.T, client *armoperationalinsights.WorkspacesClient, props *armoperationalinsights.WorkspaceProperties) *armoperationalinsights.WorkspaceProperties {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "feat-ws", armoperationalinsights.Workspace{
		Location: to.Ptr("eastus"), Properties: props,
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, testRG, "feat-ws", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	return got.Properties
}

// TestSDKWorkspaceFeaturesAndAccess covers the properties azurerm polls and
// reads back: features, public network access and the daily cap.
func TestSDKWorkspaceFeaturesAndAccess(t *testing.T) {
	client, _ := newWorkspacesClient(t)

	got := putWorkspace(t, client, &armoperationalinsights.WorkspaceProperties{})

	if got.Features == nil || got.Features.EnableLogAccessUsingOnlyResourcePermissions == nil ||
		!*got.Features.EnableLogAccessUsingOnlyResourcePermissions {
		t.Fatalf("default features = %+v, want enableLogAccessUsingOnlyResourcePermissions=true", got.Features)
	}

	if got.PublicNetworkAccessForIngestion == nil || *got.PublicNetworkAccessForIngestion != "Enabled" ||
		got.PublicNetworkAccessForQuery == nil || *got.PublicNetworkAccessForQuery != "Enabled" {
		t.Errorf("default access = %v/%v, want Enabled", got.PublicNetworkAccessForIngestion, got.PublicNetworkAccessForQuery)
	}

	if got.WorkspaceCapping == nil || got.WorkspaceCapping.DailyQuotaGb == nil || *got.WorkspaceCapping.DailyQuotaGb != -1 {
		t.Errorf("default capping = %+v, want dailyQuotaGb -1", got.WorkspaceCapping)
	}

	disabled := armoperationalinsights.PublicNetworkAccessTypeDisabled

	got = putWorkspace(t, client, &armoperationalinsights.WorkspaceProperties{
		Features: &armoperationalinsights.WorkspaceFeatures{
			EnableLogAccessUsingOnlyResourcePermissions: to.Ptr(false),
			DisableLocalAuth: to.Ptr(true),
		},
		PublicNetworkAccessForIngestion: &disabled,
		WorkspaceCapping:                &armoperationalinsights.WorkspaceCapping{DailyQuotaGb: to.Ptr(2.5)},
	})

	if *got.Features.EnableLogAccessUsingOnlyResourcePermissions || got.Features.DisableLocalAuth == nil ||
		!*got.Features.DisableLocalAuth {
		t.Errorf("features = %+v, want resource-only false and local auth disabled", got.Features)
	}

	if *got.PublicNetworkAccessForIngestion != disabled || *got.PublicNetworkAccessForQuery != "Enabled" {
		t.Errorf("access = %s/%s, want Disabled/Enabled", *got.PublicNetworkAccessForIngestion, *got.PublicNetworkAccessForQuery)
	}

	if *got.WorkspaceCapping.DailyQuotaGb != 2.5 {
		t.Errorf("dailyQuotaGb = %v, want 2.5", *got.WorkspaceCapping.DailyQuotaGb)
	}
}
